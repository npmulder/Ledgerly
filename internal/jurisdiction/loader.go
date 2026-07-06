package jurisdiction

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed packs
var embeddedPacks embed.FS

var (
	selectorPattern = regexp.MustCompile(`\A([a-z0-9][a-z0-9-]*)@([A-Za-z0-9][A-Za-z0-9._-]*)\z`)
	taxYearPattern  = regexp.MustCompile(`\A[0-9]{4}-[0-9]{2}\z`)

	activePack struct {
		sync.RWMutex
		pack *Pack
	}
)

// ValidationError reports pack load and validation failures with the embedded
// file, typed path, and failing field.
type ValidationError struct {
	File    string
	Path    string
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("%s: field %s: %s", e.File, e.Field, e.Message)
	}
	return fmt.Sprintf("%s: %s: field %s: %s", e.File, e.Path, e.Field, e.Message)
}

// ActivePack returns metadata for the pack selected during startup.
func ActivePack() PackMeta {
	activePack.RLock()
	defer activePack.RUnlock()

	if activePack.pack == nil {
		return PackMeta{}
	}
	return activePack.pack.Meta
}

// LoadActive loads, validates, freezes, and installs the selected embedded pack.
func LoadActive(selector string) error {
	return LoadActiveFromFS(embeddedPacks, selector)
}

// LoadActiveFromFS is equivalent to LoadActive, with an injected filesystem for
// tests.
func LoadActiveFromFS(files fs.FS, selector string) error {
	pack, err := LoadFromFS(files, selector)
	if err != nil {
		return err
	}

	activePack.Lock()
	defer activePack.Unlock()
	activePack.pack = clonePack(pack)
	return nil
}

// LoadFromFS parses and validates one pack from the supplied filesystem.
func LoadFromFS(files fs.FS, selector string) (*Pack, error) {
	id, version, err := parseSelector(selector)
	if err != nil {
		return nil, ValidationError{
			File:    "LEDGERLY_JURISDICTION",
			Path:    "jurisdiction",
			Field:   "selector",
			Message: err.Error(),
		}
	}

	file := PackPath(id, version)
	data, err := fs.ReadFile(files, file)
	if err != nil {
		return nil, fieldError(file, "pack", "pack", fmt.Sprintf("read embedded pack: %v", err))
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	var pack Pack
	if err := decoder.Decode(&pack); err != nil {
		return nil, fieldError(file, "pack", "yaml", err.Error())
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != nil && err != io.EOF {
		return nil, fieldError(file, "pack", "yaml", err.Error())
	}
	if extra.Kind != 0 {
		return nil, fieldError(file, "pack", "yaml", "multiple YAML documents are not supported")
	}

	if err := validatePack(file, id, version, &pack); err != nil {
		return nil, err
	}

	return clonePack(&pack), nil
}

// PackPath returns the embedded path for a jurisdiction pack selector.
func PackPath(id, version string) string {
	return filepath.ToSlash(filepath.Join("packs", id, version, "pack.yaml"))
}

func parseSelector(selector string) (string, string, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		selector = DefaultSelector
	}

	matches := selectorPattern.FindStringSubmatch(selector)
	if matches == nil {
		return "", "", fmt.Errorf("must be <jurisdiction>@<version>")
	}

	return matches[1], matches[2], nil
}

func fieldError(file, path, field, message string) ValidationError {
	return ValidationError{
		File:    file,
		Path:    path,
		Field:   field,
		Message: message,
	}
}

func clonePack(in *Pack) *Pack {
	if in == nil {
		return nil
	}

	out := *in
	out.Tax.CorporateIncome = cloneMap(in.Tax.CorporateIncome)
	out.Tax.PersonalIncome = make(map[string]PersonalIncomeYear, len(in.Tax.PersonalIncome))
	for year, value := range in.Tax.PersonalIncome {
		out.Tax.PersonalIncome[year] = clonePersonalIncomeYear(value)
	}
	out.Tax.Dividends = cloneMap(in.Tax.Dividends)
	out.Tax.VAT.Years = cloneMap(in.Tax.VAT.Years)
	out.Tax.VAT.ReverseCharge = cloneMap(in.Tax.VAT.ReverseCharge)
	out.Filings = cloneMap(in.Filings)
	out.AdvisorRules = cloneAdvisorRules(in.AdvisorRules)
	return &out
}

func clonePersonalIncomeYear(in PersonalIncomeYear) PersonalIncomeYear {
	out := in
	out.Bands = make([]TaxBand, len(in.Bands))
	for index, band := range in.Bands {
		out.Bands[index] = band
		if band.UpToMinorUnits != nil {
			upTo := *band.UpToMinorUnits
			out.Bands[index].UpToMinorUnits = &upTo
		}
	}
	return out
}

func cloneMap[K comparable, V any](in map[K]V) map[K]V {
	if in == nil {
		return nil
	}
	out := make(map[K]V, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneAdvisorRules(in []AdvisorRule) []AdvisorRule {
	if in == nil {
		return nil
	}
	out := make([]AdvisorRule, len(in))
	for index, rule := range in {
		out[index] = rule
		out[index].Surfaces = append([]string(nil), rule.Surfaces...)
		out[index].FactQuery = append([]string(nil), rule.FactQuery...)
		out[index].CTA.Params = cloneAnyMap(rule.CTA.Params)
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = cloneAnyValue(value)
	}
	return out
}

func cloneAnyValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneAnyMap(typed)
	case []any:
		out := make([]any, len(typed))
		for index, nested := range typed {
			out[index] = cloneAnyValue(nested)
		}
		return out
	default:
		return value
	}
}
