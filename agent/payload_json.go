package agent

import (
	"bytes"
	"encoding/json"
)

func (o *ConfigOption) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID           string          `json:"id"`
		Name         string          `json:"name"`
		Description  string          `json:"description"`
		Category     string          `json:"category"`
		Type         string          `json:"type"`
		CurrentValue json.RawMessage `json:"currentValue"`
		Options      json.RawMessage `json:"options"`
	}
	if err := decodeStrictJSON(data, &wire); err != nil {
		return invalidArgument("invalid_config_option_json", err.Error())
	}

	var currentValue any
	if wire.CurrentValue != nil {
		if err := decodeStrictJSON(wire.CurrentValue, &currentValue); err != nil {
			return invalidArgument("invalid_config_value", err.Error())
		}
	}
	options, err := unmarshalConfigOptions(wire.Options)
	if err != nil {
		return err
	}

	*o = ConfigOption{
		ID:           wire.ID,
		Name:         wire.Name,
		Description:  wire.Description,
		Category:     wire.Category,
		Type:         wire.Type,
		CurrentValue: currentValue,
		Options:      options,
	}
	return validateConfigOption(*o)
}

func unmarshalConfigOptions(data json.RawMessage) (any, error) {
	if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, nil
	}
	var values []ConfigOptionValue
	if err := decodeStrictJSON(data, &values); err == nil {
		return values, nil
	}
	var groups []ConfigOptionGroup
	if err := decodeStrictJSON(data, &groups); err == nil {
		return groups, nil
	}
	return nil, invalidArgument("invalid_config_options", "config options branch is invalid")
}
