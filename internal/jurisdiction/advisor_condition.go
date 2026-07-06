package jurisdiction

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

func validateAdvisorConditionSyntax(input string) error {
	parser := newAdvisorConditionParser(input)
	return parser.parse()
}

type advisorConditionTokenKind int

const (
	advisorTokenEOF advisorConditionTokenKind = iota
	advisorTokenIdent
	advisorTokenNumber
	advisorTokenString
	advisorTokenOperator
	advisorTokenLParen
	advisorTokenRParen
	advisorTokenComma
	advisorTokenPlus
	advisorTokenMinus
)

type advisorConditionToken struct {
	kind advisorConditionTokenKind
	text string
	err  error
}

type advisorConditionLexer struct {
	input string
	pos   int
}

func (l *advisorConditionLexer) next() advisorConditionToken {
	for l.pos < len(l.input) && unicode.IsSpace(rune(l.input[l.pos])) {
		l.pos++
	}
	if l.pos >= len(l.input) {
		return advisorConditionToken{kind: advisorTokenEOF}
	}

	ch := l.input[l.pos]
	switch {
	case advisorIdentStart(ch):
		start := l.pos
		l.pos++
		for l.pos < len(l.input) && advisorIdentPart(l.input[l.pos]) {
			l.pos++
		}
		return advisorConditionToken{kind: advisorTokenIdent, text: l.input[start:l.pos]}
	case ch >= '0' && ch <= '9':
		return l.number()
	case ch == '"':
		return l.quoted()
	case ch == '(':
		l.pos++
		return advisorConditionToken{kind: advisorTokenLParen, text: "("}
	case ch == ')':
		l.pos++
		return advisorConditionToken{kind: advisorTokenRParen, text: ")"}
	case ch == ',':
		l.pos++
		return advisorConditionToken{kind: advisorTokenComma, text: ","}
	case ch == '+':
		l.pos++
		return advisorConditionToken{kind: advisorTokenPlus, text: "+"}
	case ch == '-':
		l.pos++
		return advisorConditionToken{kind: advisorTokenMinus, text: "-"}
	case ch == '=' || ch == '!' || ch == '>' || ch == '<':
		return l.operator()
	default:
		l.pos++
		return advisorConditionToken{err: fmt.Errorf("unexpected character %q", ch)}
	}
}

func (l *advisorConditionLexer) number() advisorConditionToken {
	start := l.pos
	for l.pos < len(l.input) && l.input[l.pos] >= '0' && l.input[l.pos] <= '9' {
		l.pos++
	}
	if l.pos < len(l.input) && l.input[l.pos] == '.' {
		l.pos++
		if l.pos >= len(l.input) || l.input[l.pos] < '0' || l.input[l.pos] > '9' {
			return advisorConditionToken{err: fmt.Errorf("malformed number %q", l.input[start:l.pos])}
		}
		for l.pos < len(l.input) && l.input[l.pos] >= '0' && l.input[l.pos] <= '9' {
			l.pos++
		}
	}
	return advisorConditionToken{kind: advisorTokenNumber, text: l.input[start:l.pos]}
}

func (l *advisorConditionLexer) quoted() advisorConditionToken {
	start := l.pos
	l.pos++
	escaped := false
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		l.pos++
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			if _, err := strconv.Unquote(l.input[start:l.pos]); err != nil {
				return advisorConditionToken{err: err}
			}
			return advisorConditionToken{kind: advisorTokenString, text: l.input[start:l.pos]}
		}
	}
	return advisorConditionToken{err: fmt.Errorf("unterminated string literal")}
}

func (l *advisorConditionLexer) operator() advisorConditionToken {
	start := l.pos
	l.pos++
	if l.pos < len(l.input) && l.input[l.pos] == '=' {
		l.pos++
	}
	text := l.input[start:l.pos]
	switch text {
	case "=", "==", "!=", ">", ">=", "<", "<=":
		return advisorConditionToken{kind: advisorTokenOperator, text: text}
	default:
		return advisorConditionToken{err: fmt.Errorf("unknown operator %q", text)}
	}
}

func advisorIdentStart(ch byte) bool {
	return ch == '_' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z'
}

func advisorIdentPart(ch byte) bool {
	return advisorIdentStart(ch) || ch == '.' || ch >= '0' && ch <= '9'
}

type advisorConditionParser struct {
	lexer *advisorConditionLexer
	token advisorConditionToken
}

func newAdvisorConditionParser(input string) *advisorConditionParser {
	parser := &advisorConditionParser{lexer: &advisorConditionLexer{input: strings.TrimSpace(input)}}
	parser.next()
	return parser
}

func (p *advisorConditionParser) parse() error {
	if p.token.err != nil {
		return p.token.err
	}
	if err := p.parseOr(); err != nil {
		return err
	}
	if p.token.err != nil {
		return p.token.err
	}
	if p.token.kind != advisorTokenEOF {
		return fmt.Errorf("unexpected token %q", p.token.text)
	}
	return nil
}

func (p *advisorConditionParser) next() {
	p.token = p.lexer.next()
}

func (p *advisorConditionParser) parseOr() error {
	if err := p.parseAnd(); err != nil {
		return err
	}
	for p.isKeyword("or") {
		p.next()
		if err := p.parseAnd(); err != nil {
			return err
		}
	}
	return nil
}

func (p *advisorConditionParser) parseAnd() error {
	if err := p.parseNot(); err != nil {
		return err
	}
	for p.isKeyword("and") {
		p.next()
		if err := p.parseNot(); err != nil {
			return err
		}
	}
	return nil
}

func (p *advisorConditionParser) parseNot() error {
	if p.isKeyword("not") {
		p.next()
		return p.parseNot()
	}
	return p.parseComparison()
}

func (p *advisorConditionParser) parseComparison() error {
	if err := p.parseAdd(); err != nil {
		return err
	}
	if p.token.kind != advisorTokenOperator {
		return nil
	}
	p.next()
	return p.parseAdd()
}

func (p *advisorConditionParser) parseAdd() error {
	if err := p.parsePrimary(); err != nil {
		return err
	}
	for p.token.kind == advisorTokenPlus || p.token.kind == advisorTokenMinus {
		p.next()
		if err := p.parsePrimary(); err != nil {
			return err
		}
	}
	return nil
}

func (p *advisorConditionParser) parsePrimary() error {
	if p.token.err != nil {
		return p.token.err
	}
	switch p.token.kind {
	case advisorTokenIdent:
		switch p.token.text {
		case "exists":
			return p.parseExists()
		default:
			p.next()
			return nil
		}
	case advisorTokenNumber, advisorTokenString:
		p.next()
		return nil
	case advisorTokenLParen:
		p.next()
		if err := p.parseOr(); err != nil {
			return err
		}
		if p.token.kind != advisorTokenRParen {
			return fmt.Errorf("expected )")
		}
		p.next()
		return nil
	default:
		return fmt.Errorf("expected expression")
	}
}

func (p *advisorConditionParser) parseExists() error {
	p.next()
	if p.token.kind != advisorTokenLParen {
		return fmt.Errorf("expected ( after exists")
	}
	p.next()
	if p.token.kind != advisorTokenIdent {
		return fmt.Errorf("expected fact key in exists")
	}
	p.next()
	if p.token.kind != advisorTokenRParen {
		return fmt.Errorf("expected ) after exists fact")
	}
	p.next()
	return nil
}

func (p *advisorConditionParser) isKeyword(keyword string) bool {
	return p.token.kind == advisorTokenIdent && p.token.text == keyword
}
