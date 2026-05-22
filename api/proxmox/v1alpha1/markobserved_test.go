package v1alpha1

import (
	"testing"
	"time"
)

func TestVirtualMachineStatus_MarkObserved(t *testing.T) {
	s := &VirtualMachineStatus{}
	stamp := time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC)

	s.MarkObserved(stamp, 7)

	if s.LastObserved == nil {
		t.Fatal("LastObserved should be set after MarkObserved")
	}
	if !s.LastObserved.Time.Equal(stamp) {
		t.Errorf("LastObserved = %v, want %v", s.LastObserved.Time, stamp)
	}
	if s.ObservedGeneration != 7 {
		t.Errorf("ObservedGeneration = %d, want 7", s.ObservedGeneration)
	}

	// Subsequent observation advances both fields.
	later := stamp.Add(30 * time.Second)
	s.MarkObserved(later, 8)
	if !s.LastObserved.Time.Equal(later) {
		t.Errorf("LastObserved after second call = %v, want %v", s.LastObserved.Time, later)
	}
	if s.ObservedGeneration != 8 {
		t.Errorf("ObservedGeneration after second call = %d, want 8", s.ObservedGeneration)
	}
}

func TestContainerStatus_MarkObserved(t *testing.T) {
	s := &ContainerStatus{}
	stamp := time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC)

	s.MarkObserved(stamp, 3)

	if s.LastObserved == nil {
		t.Fatal("LastObserved should be set after MarkObserved")
	}
	if !s.LastObserved.Time.Equal(stamp) {
		t.Errorf("LastObserved = %v, want %v", s.LastObserved.Time, stamp)
	}
	if s.ObservedGeneration != 3 {
		t.Errorf("ObservedGeneration = %d, want 3", s.ObservedGeneration)
	}
}
