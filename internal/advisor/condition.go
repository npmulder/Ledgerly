package advisor

import (
	"fmt"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

type conditionExpr interface {
	eval(*conditionContext) (conditionValue, error)
}

type conditionContext struct {
	facts Facts
	today time.Time
}

type valueKind int

const (
	valueBool valueKind = iota
	valueString
	valueNumber
	valueMoney
	valueDate
	valueDays
)

type conditionValue struct {
	kind valueKind

	boolean bool
	text    string
	number  *big.Rat
	money   money.Money
	date    time.Time
	days    Days
}

type unknownFactError struct {
	key string
}

func (e unknownFactError) Error() string {
	return fmt.Sprintf("unknown fact %q", e.key)
}

func parseCondition(input string) (conditionExpr, error) {
	parser := newConditionParser(input)
	expr, err := parser.parse()
	if err != nil {
		return nil, err
	}
	return expr, nil
}

type literalExpr struct {
	value conditionValue
}

func (e literalExpr) eval(*conditionContext) (conditionValue, error) {
	return e.value, nil
}

type todayExpr struct{}

func (todayExpr) eval(ctx *conditionContext) (conditionValue, error) {
	return dateValue(ctx.today), nil
}

type identExpr struct {
	key string
}

func (e identExpr) eval(ctx *conditionContext) (conditionValue, error) {
	raw, ok := ctx.resolve(e.key)
	if !ok {
		return conditionValue{}, unknownFactError(e)
	}
	value, err := conditionValueFromFact(raw)
	if err != nil {
		return conditionValue{}, fmt.Errorf("fact %q: %w", e.key, err)
	}
	return value, nil
}

type existsExpr struct {
	key string
}

func (e existsExpr) eval(ctx *conditionContext) (conditionValue, error) {
	_, ok := ctx.resolve(e.key)
	return boolValue(ok), nil
}

type notExpr struct {
	expr conditionExpr
}

func (e notExpr) eval(ctx *conditionContext) (conditionValue, error) {
	value, err := e.expr.eval(ctx)
	if err != nil {
		return conditionValue{}, err
	}
	boolean, err := value.asBool()
	if err != nil {
		return conditionValue{}, err
	}
	return boolValue(!boolean), nil
}

type binaryExpr struct {
	op    string
	left  conditionExpr
	right conditionExpr
}

func (e binaryExpr) eval(ctx *conditionContext) (conditionValue, error) {
	switch e.op {
	case "and":
		left, err := e.left.eval(ctx)
		if err != nil {
			return conditionValue{}, err
		}
		leftBool, err := left.asBool()
		if err != nil {
			return conditionValue{}, err
		}
		if !leftBool {
			return boolValue(false), nil
		}
		right, err := e.right.eval(ctx)
		if err != nil {
			return conditionValue{}, err
		}
		rightBool, err := right.asBool()
		if err != nil {
			return conditionValue{}, err
		}
		return boolValue(rightBool), nil
	case "or":
		left, err := e.left.eval(ctx)
		if err != nil {
			return conditionValue{}, err
		}
		leftBool, err := left.asBool()
		if err != nil {
			return conditionValue{}, err
		}
		if leftBool {
			return boolValue(true), nil
		}
		right, err := e.right.eval(ctx)
		if err != nil {
			return conditionValue{}, err
		}
		rightBool, err := right.asBool()
		if err != nil {
			return conditionValue{}, err
		}
		return boolValue(rightBool), nil
	case "+", "-":
		left, err := e.left.eval(ctx)
		if err != nil {
			return conditionValue{}, err
		}
		right, err := e.right.eval(ctx)
		if err != nil {
			return conditionValue{}, err
		}
		return arithmeticValues(e.op, left, right)
	default:
		left, err := e.left.eval(ctx)
		if err != nil {
			return conditionValue{}, err
		}
		right, err := e.right.eval(ctx)
		if err != nil {
			return conditionValue{}, err
		}
		return compareValues(e.op, left, right)
	}
}

func (ctx *conditionContext) resolve(key string) (any, bool) {
	if value, ok := ctx.facts[FactKey(key)]; ok {
		return value, true
	}
	parts := strings.Split(key, ".")
	if len(parts) == 1 {
		return nil, false
	}
	root, ok := ctx.facts[FactKey(parts[0])]
	if !ok {
		return nil, false
	}
	current := root
	for _, part := range parts[1:] {
		next, ok := resolvePart(current, part)
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

func resolvePart(value any, part string) (any, bool) {
	if value == nil {
		return nil, false
	}
	switch typed := value.(type) {
	case map[string]any:
		next, ok := typed[part]
		return next, ok
	case map[string]FactValue:
		next, ok := typed[part]
		return next, ok
	}

	reflected := reflect.ValueOf(value)
	for reflected.Kind() == reflect.Pointer || reflected.Kind() == reflect.Interface {
		if reflected.IsNil() {
			return nil, false
		}
		reflected = reflected.Elem()
	}
	switch reflected.Kind() {
	case reflect.Map:
		keyType := reflected.Type().Key()
		if keyType.Kind() != reflect.String {
			return nil, false
		}
		key := reflect.ValueOf(part)
		if !key.Type().AssignableTo(keyType) {
			if !key.Type().ConvertibleTo(keyType) {
				return nil, false
			}
			key = key.Convert(keyType)
		}
		next := reflected.MapIndex(key)
		if !next.IsValid() {
			return nil, false
		}
		return next.Interface(), true
	case reflect.Struct:
		field, ok := structField(reflected, part)
		if !ok {
			return nil, false
		}
		return field.Interface(), true
	default:
		return nil, false
	}
}

func structField(value reflect.Value, name string) (reflect.Value, bool) {
	valueType := value.Type()
	for i := 0; i < value.NumField(); i++ {
		fieldType := valueType.Field(i)
		if !fieldType.IsExported() {
			continue
		}
		if fieldMatches(fieldType, name) {
			return value.Field(i), true
		}
	}
	return reflect.Value{}, false
}

func fieldMatches(field reflect.StructField, name string) bool {
	if field.Name == name || lowerCamel(field.Name) == name {
		return true
	}
	for _, tagName := range []string{"json", "yaml"} {
		tag := field.Tag.Get(tagName)
		if tag == "" || tag == "-" {
			continue
		}
		tag = strings.Split(tag, ",")[0]
		if tag == name {
			return true
		}
	}
	return false
}

func lowerCamel(name string) string {
	if name == "" {
		return ""
	}
	runes := []rune(name)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

func conditionValueFromFact(raw any) (conditionValue, error) {
	switch value := raw.(type) {
	case conditionValue:
		return value, nil
	case bool:
		return boolValue(value), nil
	case string:
		return stringValue(value), nil
	case money.Money:
		return moneyValue(value), nil
	case time.Time:
		return dateValue(value), nil
	case Days:
		return daysValue(value), nil
	case time.Duration:
		return daysValue(Days(value / (24 * time.Hour))), nil
	}

	number, ok := numberValueFromAny(raw)
	if ok {
		return number, nil
	}
	return conditionValue{}, fmt.Errorf("unsupported value type %T", raw)
}

func numberValueFromAny(raw any) (conditionValue, bool) {
	switch value := raw.(type) {
	case int:
		return numberValue(big.NewRat(int64(value), 1)), true
	case int8:
		return numberValue(big.NewRat(int64(value), 1)), true
	case int16:
		return numberValue(big.NewRat(int64(value), 1)), true
	case int32:
		return numberValue(big.NewRat(int64(value), 1)), true
	case int64:
		return numberValue(big.NewRat(value, 1)), true
	case uint:
		return numberValue(new(big.Rat).SetUint64(uint64(value))), true
	case uint8:
		return numberValue(new(big.Rat).SetUint64(uint64(value))), true
	case uint16:
		return numberValue(new(big.Rat).SetUint64(uint64(value))), true
	case uint32:
		return numberValue(new(big.Rat).SetUint64(uint64(value))), true
	case uint64:
		return numberValue(new(big.Rat).SetUint64(value)), true
	case float32:
		return floatValue(float64(value))
	case float64:
		return floatValue(value)
	default:
		return conditionValue{}, false
	}
}

func floatValue(value float64) (conditionValue, bool) {
	rat := new(big.Rat)
	if rat.SetFloat64(value) == nil {
		return conditionValue{}, false
	}
	return numberValue(rat), true
}

func boolValue(value bool) conditionValue {
	return conditionValue{kind: valueBool, boolean: value}
}

func stringValue(value string) conditionValue {
	return conditionValue{kind: valueString, text: value}
}

func numberValue(value *big.Rat) conditionValue {
	return conditionValue{kind: valueNumber, number: new(big.Rat).Set(value)}
}

func moneyValue(value money.Money) conditionValue {
	return conditionValue{kind: valueMoney, money: value}
}

func dateValue(value time.Time) conditionValue {
	return conditionValue{kind: valueDate, date: dateOnly(value)}
}

func daysValue(value Days) conditionValue {
	return conditionValue{kind: valueDays, days: value}
}

func (v conditionValue) asBool() (bool, error) {
	if v.kind != valueBool {
		return false, fmt.Errorf("condition value is %s, want bool", v.kindName())
	}
	return v.boolean, nil
}

func (v conditionValue) kindName() string {
	switch v.kind {
	case valueBool:
		return "bool"
	case valueString:
		return "string"
	case valueNumber:
		return "number"
	case valueMoney:
		return "money"
	case valueDate:
		return "date"
	case valueDays:
		return "days"
	default:
		return "unknown"
	}
}

func arithmeticValues(op string, left, right conditionValue) (conditionValue, error) {
	if left.kind == valueNumber && right.kind == valueNumber {
		out := new(big.Rat).Set(left.number)
		if op == "+" {
			out.Add(out, right.number)
		} else {
			out.Sub(out, right.number)
		}
		return numberValue(out), nil
	}
	if left.kind == valueDays && right.kind == valueDays {
		if op == "+" {
			return daysValue(left.days + right.days), nil
		}
		return daysValue(left.days - right.days), nil
	}
	if left.kind == valueDate && right.kind == valueDate && op == "-" {
		return daysValue(daysBetween(left.date, right.date)), nil
	}
	if left.kind == valueDate {
		days, ok := valueAsDays(right)
		if ok {
			if op == "+" {
				return dateValue(left.date.AddDate(0, 0, int(days))), nil
			}
			return dateValue(left.date.AddDate(0, 0, -int(days))), nil
		}
	}
	if right.kind == valueDate && op == "+" {
		days, ok := valueAsDays(left)
		if ok {
			return dateValue(right.date.AddDate(0, 0, int(days))), nil
		}
	}
	if left.kind == valueMoney && right.kind == valueMoney {
		var (
			out money.Money
			err error
		)
		if op == "+" {
			out, err = left.money.Add(right.money)
		} else {
			out, err = left.money.Sub(right.money)
		}
		if err != nil {
			return conditionValue{}, err
		}
		return moneyValue(out), nil
	}
	return conditionValue{}, fmt.Errorf("operator %s not supported for %s and %s", op, left.kindName(), right.kindName())
}

func compareValues(op string, left, right conditionValue) (conditionValue, error) {
	if op == "==" {
		op = "="
	}

	var cmp int
	var ok bool
	switch {
	case left.kind == valueNumber && right.kind == valueNumber:
		cmp = left.number.Cmp(right.number)
		ok = true
	case left.kind == valueDays && right.kind == valueDays:
		cmp = compareInts(int(left.days), int(right.days))
		ok = true
	case left.kind == valueDays && right.kind == valueNumber:
		cmp = big.NewRat(int64(left.days), 1).Cmp(right.number)
		ok = true
	case left.kind == valueNumber && right.kind == valueDays:
		cmp = left.number.Cmp(big.NewRat(int64(right.days), 1))
		ok = true
	case left.kind == valueMoney && right.kind == valueMoney:
		moneyCmp, err := left.money.Cmp(right.money)
		if err != nil {
			return conditionValue{}, err
		}
		cmp = moneyCmp
		ok = true
	case left.kind == valueDate && right.kind == valueDate:
		cmp = left.date.Compare(right.date)
		ok = true
	case left.kind == valueString && right.kind == valueString:
		cmp = strings.Compare(left.text, right.text)
		ok = true
	case left.kind == valueBool && right.kind == valueBool:
		cmp = compareBools(left.boolean, right.boolean)
		ok = true
	}
	if !ok {
		return conditionValue{}, fmt.Errorf("operator %s not supported for %s and %s", op, left.kindName(), right.kindName())
	}

	switch op {
	case "=":
		return boolValue(cmp == 0), nil
	case "!=":
		return boolValue(cmp != 0), nil
	case ">":
		return boolValue(cmp > 0), nil
	case ">=":
		return boolValue(cmp >= 0), nil
	case "<":
		return boolValue(cmp < 0), nil
	case "<=":
		return boolValue(cmp <= 0), nil
	default:
		return conditionValue{}, fmt.Errorf("unknown comparison operator %q", op)
	}
}

func valueAsDays(value conditionValue) (Days, bool) {
	switch value.kind {
	case valueDays:
		return value.days, true
	case valueNumber:
		if !value.number.IsInt() {
			return 0, false
		}
		return Days(value.number.Num().Int64()), true
	default:
		return 0, false
	}
}

func compareInts(left, right int) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func compareBools(left, right bool) int {
	switch {
	case left == right:
		return 0
	case !left:
		return -1
	default:
		return 1
	}
}

func daysBetween(left, right time.Time) Days {
	return Days(int(dateOnly(left).Sub(dateOnly(right)) / (24 * time.Hour)))
}

func dateOnly(value time.Time) time.Time {
	year, month, day := value.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

type tokenKind int

const (
	tokenEOF tokenKind = iota
	tokenIdent
	tokenNumber
	tokenString
	tokenOperator
	tokenLParen
	tokenRParen
	tokenComma
	tokenPlus
	tokenMinus
)

type conditionToken struct {
	kind tokenKind
	text string
	err  error
}

type conditionLexer struct {
	input string
	pos   int
}

func (l *conditionLexer) next() conditionToken {
	for l.pos < len(l.input) && unicode.IsSpace(rune(l.input[l.pos])) {
		l.pos++
	}
	if l.pos >= len(l.input) {
		return conditionToken{kind: tokenEOF}
	}

	ch := l.input[l.pos]
	switch {
	case isIdentStart(ch):
		start := l.pos
		l.pos++
		for l.pos < len(l.input) && isIdentPart(l.input[l.pos]) {
			l.pos++
		}
		return conditionToken{kind: tokenIdent, text: l.input[start:l.pos]}
	case ch >= '0' && ch <= '9':
		return l.number()
	case ch == '"':
		return l.quoted()
	case ch == '(':
		l.pos++
		return conditionToken{kind: tokenLParen, text: "("}
	case ch == ')':
		l.pos++
		return conditionToken{kind: tokenRParen, text: ")"}
	case ch == ',':
		l.pos++
		return conditionToken{kind: tokenComma, text: ","}
	case ch == '+':
		l.pos++
		return conditionToken{kind: tokenPlus, text: "+"}
	case ch == '-':
		l.pos++
		return conditionToken{kind: tokenMinus, text: "-"}
	case ch == '=' || ch == '!' || ch == '>' || ch == '<':
		return l.operator()
	default:
		l.pos++
		return conditionToken{err: fmt.Errorf("unexpected character %q", ch)}
	}
}

func (l *conditionLexer) number() conditionToken {
	start := l.pos
	for l.pos < len(l.input) && l.input[l.pos] >= '0' && l.input[l.pos] <= '9' {
		l.pos++
	}
	if l.pos < len(l.input) && l.input[l.pos] == '.' {
		l.pos++
		if l.pos >= len(l.input) || l.input[l.pos] < '0' || l.input[l.pos] > '9' {
			return conditionToken{err: fmt.Errorf("malformed number %q", l.input[start:l.pos])}
		}
		for l.pos < len(l.input) && l.input[l.pos] >= '0' && l.input[l.pos] <= '9' {
			l.pos++
		}
	}
	return conditionToken{kind: tokenNumber, text: l.input[start:l.pos]}
}

func (l *conditionLexer) quoted() conditionToken {
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
			text, err := strconv.Unquote(l.input[start:l.pos])
			if err != nil {
				return conditionToken{err: err}
			}
			return conditionToken{kind: tokenString, text: text}
		}
	}
	return conditionToken{err: fmt.Errorf("unterminated string literal")}
}

func (l *conditionLexer) operator() conditionToken {
	start := l.pos
	l.pos++
	if l.pos < len(l.input) && l.input[l.pos] == '=' {
		l.pos++
	}
	text := l.input[start:l.pos]
	switch text {
	case "=", "==", "!=", ">", ">=", "<", "<=":
		return conditionToken{kind: tokenOperator, text: text}
	default:
		return conditionToken{err: fmt.Errorf("unknown operator %q", text)}
	}
}

func isIdentStart(ch byte) bool {
	return ch == '_' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z'
}

func isIdentPart(ch byte) bool {
	return isIdentStart(ch) || ch == '.' || ch >= '0' && ch <= '9'
}

type conditionParser struct {
	lexer *conditionLexer
	token conditionToken
}

func newConditionParser(input string) *conditionParser {
	parser := &conditionParser{lexer: &conditionLexer{input: strings.TrimSpace(input)}}
	parser.next()
	return parser
}

func (p *conditionParser) parse() (conditionExpr, error) {
	if p.token.err != nil {
		return nil, p.token.err
	}
	expr, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.token.err != nil {
		return nil, p.token.err
	}
	if p.token.kind != tokenEOF {
		return nil, fmt.Errorf("unexpected token %q", p.token.text)
	}
	return expr, nil
}

func (p *conditionParser) next() {
	p.token = p.lexer.next()
}

func (p *conditionParser) parseOr() (conditionExpr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.isKeyword("or") {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = binaryExpr{op: "or", left: left, right: right}
	}
	return left, nil
}

func (p *conditionParser) parseAnd() (conditionExpr, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.isKeyword("and") {
		p.next()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = binaryExpr{op: "and", left: left, right: right}
	}
	return left, nil
}

func (p *conditionParser) parseNot() (conditionExpr, error) {
	if p.isKeyword("not") {
		p.next()
		expr, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return notExpr{expr: expr}, nil
	}
	return p.parseComparison()
}

func (p *conditionParser) parseComparison() (conditionExpr, error) {
	left, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	if p.token.kind != tokenOperator {
		return left, nil
	}
	op := p.token.text
	p.next()
	right, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	return binaryExpr{op: op, left: left, right: right}, nil
}

func (p *conditionParser) parseAdd() (conditionExpr, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for p.token.kind == tokenPlus || p.token.kind == tokenMinus {
		op := p.token.text
		p.next()
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		left = binaryExpr{op: op, left: left, right: right}
	}
	return left, nil
}

func (p *conditionParser) parsePrimary() (conditionExpr, error) {
	if p.token.err != nil {
		return nil, p.token.err
	}
	switch p.token.kind {
	case tokenIdent:
		text := p.token.text
		switch text {
		case "exists":
			return p.parseExists()
		case "today":
			p.next()
			return todayExpr{}, nil
		case "true":
			p.next()
			return literalExpr{value: boolValue(true)}, nil
		case "false":
			p.next()
			return literalExpr{value: boolValue(false)}, nil
		default:
			p.next()
			return identExpr{key: text}, nil
		}
	case tokenNumber:
		number, ok := new(big.Rat).SetString(p.token.text)
		if !ok {
			return nil, fmt.Errorf("malformed number %q", p.token.text)
		}
		p.next()
		return literalExpr{value: numberValue(number)}, nil
	case tokenString:
		text := p.token.text
		p.next()
		return literalExpr{value: stringValue(text)}, nil
	case tokenLParen:
		p.next()
		expr, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.token.kind != tokenRParen {
			return nil, fmt.Errorf("expected )")
		}
		p.next()
		return expr, nil
	default:
		return nil, fmt.Errorf("expected expression")
	}
}

func (p *conditionParser) parseExists() (conditionExpr, error) {
	p.next()
	if p.token.kind != tokenLParen {
		return nil, fmt.Errorf("expected ( after exists")
	}
	p.next()
	if p.token.kind != tokenIdent {
		return nil, fmt.Errorf("expected fact key in exists")
	}
	key := p.token.text
	p.next()
	if p.token.kind != tokenRParen {
		return nil, fmt.Errorf("expected ) after exists fact")
	}
	p.next()
	return existsExpr{key: key}, nil
}

func (p *conditionParser) isKeyword(keyword string) bool {
	return p.token.kind == tokenIdent && p.token.text == keyword
}
