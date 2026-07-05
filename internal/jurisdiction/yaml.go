package jurisdiction

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

func (v *VAT) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("vat must be a mapping")
	}

	v.Years = make(map[string]VATYear)
	for index := 0; index < len(value.Content); index += 2 {
		key := value.Content[index].Value
		node := value.Content[index+1]

		switch key {
		case "regime":
			if err := node.Decode(&v.Regime); err != nil {
				return err
			}
		case "reverse_charge":
			if err := node.Decode(&v.ReverseCharge); err != nil {
				return err
			}
		default:
			if !taxYearPattern.MatchString(key) {
				return fmt.Errorf("unknown vat field %q", key)
			}
			var year VATYear
			if err := node.Decode(&year); err != nil {
				return err
			}
			v.Years[key] = year
		}
	}

	return nil
}
