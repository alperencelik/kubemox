package proxmox

import (
	"errors"
	"fmt"
	"strings"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	proxmox "github.com/luthermonson/go-proxmox"
)

// containerUIDTagPrefix starts the Proxmox tag that binds a container to the
// Container object that created or adopted it: kubemox-uid-<metadata.uid>.
//
// Proxmox guarantees a unique VMID, not a unique hostname, so two objects
// with the same spec.name used to resolve to one container, and deleting
// either destroyed it. The tag lets every lookup, by hostname or by VMID,
// tell whose container it found.
const containerUIDTagPrefix = "kubemox-uid-"

// containerOptionTags is the container config key that holds the tags.
const containerOptionTags = "tags"

// ContainerUIDTag is the tag that marks a Proxmox container as ct's.
func ContainerUIDTag(ct *proxmoxv1alpha1.Container) string {
	return containerUIDTagPrefix + string(ct.UID)
}

// containerOwnerUID returns the UID in a kubemox-uid tag, or "" when the
// container carries none: created by an older kubemox, or by hand.
func containerOwnerUID(tags string) string {
	for _, t := range strings.Split(tags, ";") {
		if uid, ok := strings.CutPrefix(strings.TrimSpace(t), containerUIDTagPrefix); ok {
			return uid
		}
	}
	return ""
}

// withContainerUIDTag returns tags with any kubemox-uid tag replaced by ct's.
func withContainerUIDTag(tags string, ct *proxmoxv1alpha1.Container) string {
	out := []string{}
	for _, t := range strings.Split(tags, ";") {
		if t = strings.TrimSpace(t); t != "" && !strings.HasPrefix(t, containerUIDTagPrefix) {
			out = append(out, t)
		}
	}
	return strings.Join(append(out, ContainerUIDTag(ct)), ";")
}

func containerTags(c *proxmox.Container) string {
	if c.ContainerConfig == nil {
		return c.Tags
	}
	return c.ContainerConfig.Tags
}

// NameConflictError means the container kubemox would bind to an object is
// not that object's: it carries another object's UID tag, or the hostname
// matches more than one container and none carries the object's tag.
// Nothing is changed on such a container.
type NameConflictError struct {
	Message string
}

func (e *NameConflictError) Error() string {
	return e.Message
}

// IsNameConflict reports whether err is a NameConflictError.
func IsNameConflict(err error) bool {
	var conflict *NameConflictError
	return errors.As(err, &conflict)
}

// IsContainerNotFound reports whether err says that ct has no container.
func IsContainerNotFound(err error) bool {
	return isContainerGoneErr(err)
}

// pickContainerByName chooses ct's container among the ones whose hostname is
// spec.name. One carrying ct's tag wins. Without one, a single untagged
// container is adopted: that is how an object created by an older kubemox
// finds its container. Anything else is a conflict.
func pickContainerByName(ct *proxmoxv1alpha1.Container, named []*proxmox.Container) (int, error) {
	var ours, untagged, foreign []int
	for _, c := range named {
		switch containerOwnerUID(c.Tags) {
		case string(ct.UID):
			ours = append(ours, int(c.VMID))
		case "":
			untagged = append(untagged, int(c.VMID))
		default:
			foreign = append(foreign, int(c.VMID))
		}
	}
	switch {
	case len(ours) == 1:
		return ours[0], nil
	case len(ours) == 0 && len(foreign) == 0 && len(untagged) == 1:
		return untagged[0], nil
	case len(named) == 0:
		return 0, &NotFoundError{Message: fmt.Sprintf("container %s not found on node %s", ct.Spec.Name, ct.Spec.NodeName)}
	default:
		return 0, &NameConflictError{Message: fmt.Sprintf(
			"hostname %s on node %s matches containers %v, and none of them is tagged %s",
			ct.Spec.Name, ct.Spec.NodeName, append(append(ours, untagged...), foreign...), ContainerUIDTag(ct))}
	}
}

// checkContainerOwner refuses a container found by VMID that is not ct's.
// An untagged one is ct's only if its hostname is still spec.name: Proxmox
// hands out a freed VMID again, so the VMID in status may now belong to a
// container someone else made, and that one is reported as not found.
func checkContainerOwner(ct *proxmoxv1alpha1.Container, c *proxmox.Container) error {
	switch owner := containerOwnerUID(containerTags(c)); {
	case owner == string(ct.UID):
		return nil
	case owner != "":
		return &NameConflictError{Message: fmt.Sprintf(
			"container %d on node %s is tagged for the object with UID %s, not for %s",
			c.VMID, ct.Spec.NodeName, owner, ct.UID)}
	case c.ContainerConfig != nil && c.ContainerConfig.Hostname != ct.Spec.Name:
		return &NotFoundError{Message: fmt.Sprintf("container %d on node %s is now %q, not %s",
			c.VMID, ct.Spec.NodeName, c.ContainerConfig.Hostname, ct.Spec.Name)}
	default:
		return nil
	}
}
