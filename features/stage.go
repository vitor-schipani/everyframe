package features

// Stage is one part of the work Add does for a frame.
type Stage int

const (
	// StageStored counts the kinds of pixel the frame stores.
	StageStored Stage = iota
	// StageComposite draws the frame on the canvas.
	StageComposite
	// StageChange compares the picture with the previous one. It also
	// keeps up to date the brightness grid the hash is read from, which
	// is most of the cost of hashing.
	StageChange
	// StageHash computes the perceptual hash and its distances.
	StageHash
	// StageSummary adds the frame to the summary of the animation.
	StageSummary

	// NumStages is the number of stages.
	NumStages int = iota
)

var stageNames = [NumStages]string{"stored", "composite", "change", "hash", "summary"}

// String returns the stage's name, such as "stored" or "hash".
func (s Stage) String() string {
	if s < 0 || int(s) >= NumStages {
		return "unknown"
	}
	return stageNames[s]
}

// OnStage makes Add call fn each time it finishes a stage, in the order the
// stages are declared, so that a caller can measure what each one costs: the
// time or memory a stage took is what was spent since the previous call, or
// since Add was called for the first stage. A nil fn stops the calls.
func (e *Extractor) OnStage(fn func(Stage)) { e.onStage = fn }

// done reports the end of a stage to the function given to OnStage.
func (e *Extractor) done(s Stage) {
	if e.onStage != nil {
		e.onStage(s)
	}
}
