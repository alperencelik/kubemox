package v1alpha1

import "testing"

// The empty value has to behave as Full: every object written before this field
// existed omits it, and turning those into linked clones on upgrade would pin
// the template behind the operator's back.
func TestCloneModeDefaultsToFull(t *testing.T) {
	for _, tc := range []struct {
		mode     CloneMode
		wantFull bool
		wantFlag uint8
	}{
		{mode: "", wantFull: true, wantFlag: 1},
		{mode: CloneModeFull, wantFull: true, wantFlag: 1},
		{mode: CloneModeLinked, wantFull: false, wantFlag: 0},
	} {
		if got := tc.mode.IsFullClone(); got != tc.wantFull {
			t.Errorf("CloneMode(%q).IsFullClone() = %v, want %v", tc.mode, got, tc.wantFull)
		}
		if got := tc.mode.CloneFlag(); got != tc.wantFlag {
			t.Errorf("CloneMode(%q).CloneFlag() = %d, want %d", tc.mode, got, tc.wantFlag)
		}
	}
}
