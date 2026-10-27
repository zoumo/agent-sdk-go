package recording

import (
	shared "github.com/zoumo/agent-sdk-go/internal/recording"
	"io"
)

const ProtocolLimit = shared.ProtocolLimit
const StderrLimit = shared.StderrLimit

type Capture = shared.Capture
type ProxyConfig = shared.ProxyConfig
type TransportResult = shared.TransportResult

func NewCapture(w io.Writer, limit int) *Capture { return shared.NewCapture(w, limit) }
func Proxy(cfg ProxyConfig, in io.Reader, out io.Writer) (TransportResult, error) {
	return shared.Proxy(cfg, in, out)
}

func Guard(mode string) error { return shared.Guard(mode) }
