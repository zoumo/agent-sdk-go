package mocktest

import (
	"errors"
	"reflect"

	"github.com/zoumo/agent-sdk-go/replay"
)

func validateEmbeddedTestifyIdentity(tool replay.ToolIdentity) error {
	want, err := replay.BuildIdentity()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(tool, want) {
		return errors.New("embedded testify identity differs from the linked SDK build")
	}
	return nil
}
