package jurisdiction

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

func (r *Rate) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("rate must be a scalar")
	}
	if value.Tag != "!!str" {
		return fmt.Errorf("rate must be a decimal string")
	}
	*r = Rate(value.Value)
	return nil
}

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
		case "authority":
			if err := node.Decode(&v.Authority); err != nil {
				return err
			}
		case "treatments":
			if err := node.Decode(&v.Treatments); err != nil {
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

func (v *VATYear) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("vat year must be a mapping")
	}

	var seenStandardRate bool
	for index := 0; index < len(value.Content); index += 2 {
		key := value.Content[index].Value
		node := value.Content[index+1]

		switch key {
		case "standard_rate":
			if seenStandardRate {
				return fmt.Errorf("duplicate vat year field %q", key)
			}
			if err := node.Decode(&v.StandardRate); err != nil {
				return err
			}
			seenStandardRate = true
		default:
			return fmt.Errorf("unknown vat year field %q", key)
		}
	}

	if !seenStandardRate {
		return fmt.Errorf("vat year field %q is required", "standard_rate")
	}

	return nil
}
