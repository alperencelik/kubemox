package proxmox

import "fmt"

type NotFoundError struct {
	Message string
}

func (e *NotFoundError) Error() string {
	return e.Message
}

// AmbiguousNameError is returned when a name matches more than one machine.
//
// Returning one of them would mean silently choosing which of two users' VMs a
// caller operates on, and which one it picked would depend on iteration order.
type AmbiguousNameError struct {
	Name    string
	Matches []string
}

func (e *AmbiguousNameError) Error() string {
	return fmt.Sprintf("name %q matches %d virtual machines (%v); "+
		"identify it by VMID instead", e.Name, len(e.Matches), e.Matches)
}
