package plugin

import "testing"

// recordingPlugin records its execution and optionally stops the chain.
type recordingPlugin struct {
	name  string
	phase Phase
	stop  bool
	ran   *[]string
}

func (p recordingPlugin) Name() string    { return p.name }
func (p recordingPlugin) Phases() []Phase { return []Phase{p.phase} }
func (p recordingPlugin) Execute(c *Context, _ Phase) error {
	*p.ran = append(*p.ran, p.name)
	if p.stop {
		c.Stop = true
		c.BlockedBy = p.name
	}
	return nil
}

func TestChain_RunsMatchingPhaseInOrder(t *testing.T) {
	var ran []string
	chain := NewChain(
		recordingPlugin{name: "pre1", phase: PhasePre, ran: &ran},
		recordingPlugin{name: "post1", phase: PhasePost, ran: &ran},
		recordingPlugin{name: "pre2", phase: PhasePre, ran: &ran},
	)

	c := &Context{}
	if err := chain.Run(c, PhasePre); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(ran) != 2 || ran[0] != "pre1" || ran[1] != "pre2" {
		t.Errorf("pre-phase order = %v, want [pre1 pre2]", ran)
	}
}

func TestChain_StopShortCircuits(t *testing.T) {
	var ran []string
	chain := NewChain(
		recordingPlugin{name: "cache", phase: PhasePre, stop: true, ran: &ran},
		recordingPlugin{name: "ratelimit", phase: PhasePre, ran: &ran},
	)

	c := &Context{}
	if err := chain.Run(c, PhasePre); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(ran) != 1 || ran[0] != "cache" {
		t.Errorf("expected only [cache] to run, got %v", ran)
	}
	if c.BlockedBy != "cache" {
		t.Errorf("BlockedBy = %q, want %q", c.BlockedBy, "cache")
	}
}
