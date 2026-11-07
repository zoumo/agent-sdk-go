package agent

import "encoding/json"

func validateJSONValue(value any) error {
	_, err := json.Marshal(value)
	return err
}
