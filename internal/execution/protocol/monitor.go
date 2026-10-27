package protocol

type MonitorStart struct{ Command string }
type MonitorOutput struct {
	Data   []byte
	Stderr bool
}
