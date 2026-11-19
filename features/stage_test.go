package features

import (
	"image"
	"image/color"
	"reflect"
	"testing"
)

func TestOnStage(t *testing.T) {
	e := NewExtractor(header(2, 1))
	var got []Stage
	e.OnStage(func(s Stage) { got = append(got, s) })
	e.Add(frame(0, image.Rect(0, 0, 2, 1), color.Palette{black, white}, 1))
	want := []Stage{StageStored, StageComposite, StageChange, StageHash, StageSummary}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("stages = %v, want %v", got, want)
	}
	if len(want) != NumStages {
		t.Errorf("NumStages = %d, but Add has %d stages", NumStages, len(want))
	}

	e.OnStage(nil)
	e.Add(frame(1, image.Rect(0, 0, 2, 1), color.Palette{black, white}, 0))
	if len(got) != len(want) {
		t.Errorf("stages were still reported after OnStage(nil): %v", got)
	}
}

func TestStageString(t *testing.T) {
	for s, want := range map[Stage]string{StageStored: "stored", StageSummary: "summary", Stage(-1): "unknown", Stage(NumStages): "unknown"} {
		if got := s.String(); got != want {
			t.Errorf("Stage(%d).String() = %q, want %q", int(s), got, want)
		}
	}
}
