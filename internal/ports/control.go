package ports

// ControlPanel is a server-authored view of existing commands. Actions are
// opaque to the transport; they are never model-authored selection values.
type ControlPanel struct {
	Text    string
	Options []ControlOption
}

type ControlOption struct{ Label, Action string }
