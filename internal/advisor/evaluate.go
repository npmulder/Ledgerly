package advisor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

// Evaluate is pure: it reads only injected rules/facts/clock and returns the
// desired advisor insight delta for Apply to persist.
func Evaluate(rules []RuleDef, facts Facts, now time.Time) (Delta, error) {
	if facts == nil {
		facts = Facts{}
	}

	ctx := &conditionContext{
		facts: facts,
		today: dateOnly(now),
	}
	delta := Delta{GeneratedAt: now.UTC()}
	evaluated := map[string]struct{}{}

	for _, input := range rules {
		rule, err := ensureCompiled(input)
		if err != nil {
			return Delta{}, err
		}

		result, err := rule.condition.eval(ctx)
		if err != nil {
			var unknown unknownFactError
			if errors.As(err, &unknown) {
				delta.Warnings = append(delta.Warnings, Warning{
					RuleID:  rule.ID,
					Message: unknown.Error(),
				})
				continue
			}
			delta.Warnings = append(delta.Warnings, Warning{
				RuleID:  rule.ID,
				Message: fmt.Sprintf("condition skipped: %v", err),
			})
			continue
		}
		fires, err := result.asBool()
		if err != nil {
			delta.Warnings = append(delta.Warnings, Warning{
				RuleID:  rule.ID,
				Message: fmt.Sprintf("condition skipped: %v", err),
			})
			continue
		}

		evaluated[rule.ID] = struct{}{}
		if !fires {
			continue
		}

		bindings, renderBindings, canonical, err := factBindings(rule, ctx)
		if err != nil {
			delta.Warnings = append(delta.Warnings, Warning{
				RuleID:  rule.ID,
				Message: fmt.Sprintf("bindings skipped: %v", err),
			})
			delete(evaluated, rule.ID)
			continue
		}
		rendered, err := renderText(rule, renderBindings)
		if err != nil {
			delta.Warnings = append(delta.Warnings, Warning{
				RuleID:  rule.ID,
				Message: fmt.Sprintf("template skipped: %v", err),
			})
			delete(evaluated, rule.ID)
			continue
		}
		cta, err := renderCTA(rule.CTA, renderBindings)
		if err != nil {
			delta.Warnings = append(delta.Warnings, Warning{
				RuleID:  rule.ID,
				Message: fmt.Sprintf("cta skipped: %v", err),
			})
			delete(evaluated, rule.ID)
			continue
		}

		hashBytes := sha256.Sum256(canonical)
		factHash := hex.EncodeToString(hashBytes[:])
		delta.Insights = append(delta.Insights, Insight{
			Key:          InsightKey(rule.ID + ":" + factHash),
			RuleID:       rule.ID,
			FactHash:     factHash,
			Severity:     rule.Severity,
			Surfaces:     append([]Surface(nil), rule.Surfaces...),
			RenderedText: rendered,
			Bindings:     bindings,
			CTA:          cta,
			CreatedAt:    now.UTC(),
		})
	}

	for ruleID := range evaluated {
		delta.EvaluatedRuleIDs = append(delta.EvaluatedRuleIDs, ruleID)
	}
	sort.Slice(delta.Insights, func(i, j int) bool {
		return delta.Insights[i].Key < delta.Insights[j].Key
	})
	sort.Strings(delta.EvaluatedRuleIDs)
	sort.Slice(delta.Warnings, func(i, j int) bool {
		if delta.Warnings[i].RuleID == delta.Warnings[j].RuleID {
			return delta.Warnings[i].Message < delta.Warnings[j].Message
		}
		return delta.Warnings[i].RuleID < delta.Warnings[j].RuleID
	})
	return delta, nil
}

func ensureCompiled(rule RuleDef) (RuleDef, error) {
	if rule.condition != nil {
		return rule, nil
	}
	return CompileRule(rule)
}

func factBindings(rule RuleDef, ctx *conditionContext) (map[string]any, map[string]any, []byte, error) {
	bindings := make(map[string]any, len(rule.FactQuery))
	renderBindings := make(map[string]any, len(rule.FactQuery))
	for _, key := range rule.FactQuery {
		raw, ok := ctx.resolve(string(key))
		if !ok {
			return nil, nil, nil, unknownFactError{key: string(key)}
		}
		value, err := canonicalValue(raw)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("fact %q: %w", key, err)
		}
		bindings[string(key)] = value
		renderBindings[string(key)] = raw
	}
	canonical, err := json.Marshal(bindings)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("marshal canonical bindings: %w", err)
	}
	return bindings, renderBindings, canonical, nil
}

func canonicalValue(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch typed := value.(type) {
	case bool, string:
		return typed, nil
	case int:
		return typed, nil
	case int8:
		return int64(typed), nil
	case int16:
		return int64(typed), nil
	case int32:
		return int64(typed), nil
	case int64:
		return typed, nil
	case uint:
		return uint64(typed), nil
	case uint8:
		return uint64(typed), nil
	case uint16:
		return uint64(typed), nil
	case uint32:
		return uint64(typed), nil
	case uint64:
		return typed, nil
	case float32:
		return float64(typed), nil
	case float64:
		return typed, nil
	case money.Money:
		return map[string]any{
			"amount":   typed.Amount,
			"currency": typed.Currency,
		}, nil
	case time.Time:
		return dateOnly(typed).Format(time.DateOnly), nil
	case Days:
		return int(typed), nil
	case time.Duration:
		return int(typed / (24 * time.Hour)), nil
	case map[string]any:
		return canonicalStringMap(typed)
	case map[string]FactValue:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			value, err := canonicalValue(nested)
			if err != nil {
				return nil, err
			}
			out[key] = value
		}
		return out, nil
	}
	return canonicalReflectValue(reflect.ValueOf(value))
}

func canonicalStringMap(in map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(in))
	for key, nested := range in {
		value, err := canonicalValue(nested)
		if err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, nil
}

func canonicalReflectValue(value reflect.Value) (any, error) {
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return nil, nil
		}
		value = value.Elem()
	}
	if value.CanInterface() {
		switch typed := value.Interface().(type) {
		case time.Time:
			return dateOnly(typed).Format(time.DateOnly), nil
		case money.Money:
			return map[string]any{
				"amount":   typed.Amount,
				"currency": typed.Currency,
			}, nil
		case Days:
			return int(typed), nil
		}
	}
	switch value.Kind() {
	case reflect.Bool:
		return value.Bool(), nil
	case reflect.String:
		return value.String(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return value.Uint(), nil
	case reflect.Float32, reflect.Float64:
		return value.Float(), nil
	case reflect.Slice, reflect.Array:
		out := make([]any, value.Len())
		for index := 0; index < value.Len(); index++ {
			nested, err := canonicalReflectValue(value.Index(index))
			if err != nil {
				return nil, err
			}
			out[index] = nested
		}
		return out, nil
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("map key type %s is unsupported", value.Type().Key())
		}
		out := make(map[string]any, value.Len())
		for _, key := range value.MapKeys() {
			nested, err := canonicalReflectValue(value.MapIndex(key))
			if err != nil {
				return nil, err
			}
			out[key.String()] = nested
		}
		return out, nil
	case reflect.Struct:
		return canonicalStruct(value)
	default:
		return nil, fmt.Errorf("unsupported value type %s", value.Type())
	}
}

func canonicalStruct(value reflect.Value) (map[string]any, error) {
	valueType := value.Type()
	out := map[string]any{}
	for index := 0; index < value.NumField(); index++ {
		fieldType := valueType.Field(index)
		if !fieldType.IsExported() {
			continue
		}
		key := structFieldName(fieldType)
		if key == "" {
			continue
		}
		nested, err := canonicalReflectValue(value.Field(index))
		if err != nil {
			return nil, err
		}
		out[key] = nested
	}
	return out, nil
}

func structFieldName(field reflect.StructField) string {
	for _, tagName := range []string{"json", "yaml"} {
		tag := field.Tag.Get(tagName)
		if tag == "-" {
			return ""
		}
		if tag != "" {
			name := strings.Split(tag, ",")[0]
			if name != "" {
				return name
			}
		}
	}
	return lowerCamel(field.Name)
}

func renderText(rule RuleDef, bindings map[string]any) (string, error) {
	return renderTemplateString(rule.ID, rule.TextTemplate, bindings)
}

func renderCTA(cta CTA, bindings map[string]any) (CTA, error) {
	out := cta
	out.Params = nil
	renderedAction, err := renderTemplateString("cta.action", cta.Action, bindings)
	if err != nil {
		return CTA{}, err
	}
	out.Action = renderedAction
	if len(cta.Params) == 0 {
		return out, nil
	}
	params := make(map[string]any, len(cta.Params))
	for key, value := range cta.Params {
		rendered, err := renderCTAValue("cta."+key, value, bindings)
		if err != nil {
			return CTA{}, err
		}
		params[key] = rendered
	}
	out.Params = params
	return out, nil
}

func renderCTAValue(name string, value any, bindings map[string]any) (any, error) {
	switch typed := value.(type) {
	case string:
		return renderTemplateString(name, typed, bindings)
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			rendered, err := renderCTAValue(name+"."+key, nested, bindings)
			if err != nil {
				return nil, err
			}
			out[key] = rendered
		}
		return out, nil
	case []any:
		out := make([]any, len(typed))
		for index, nested := range typed {
			rendered, err := renderCTAValue(fmt.Sprintf("%s.%d", name, index), nested, bindings)
			if err != nil {
				return nil, err
			}
			out[index] = rendered
		}
		return out, nil
	default:
		return typed, nil
	}
}

func renderTemplateString(name, input string, bindings map[string]any) (string, error) {
	formatted := make(map[string]any, len(bindings))
	funcs := template.FuncMap{}
	for key, value := range bindings {
		formattedValue := templateValue(value)
		formatted[key] = formattedValue
		if templateIdentPattern.MatchString(key) {
			funcs[key] = func(value any) func() any {
				return func() any { return value }
			}(formattedValue)
		}
	}

	tmpl, err := template.New(name).Option("missingkey=error").Funcs(funcs).Parse(input)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, formatted); err != nil {
		return "", err
	}
	return out.String(), nil
}

func templateValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			out[key] = templateValue(nested)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, nested := range typed {
			out[index] = templateValue(nested)
		}
		return out
	default:
		return formatFactValue(typed)
	}
}
