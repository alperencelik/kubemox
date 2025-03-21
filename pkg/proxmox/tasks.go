package proxmox

import (
	"github.com/luthermonson/go-proxmox"
)

type TaskError struct {
	ExitStatus string
}

func (e *TaskError) Error() string {
	return e.ExitStatus
}

func (pi *ProxmoxInstance) GetTask(taskUPID string) *proxmox.Task {
	taskID := proxmox.UPID(
		taskUPID,
	)
	return proxmox.NewTask(taskID, pi.Client)
}
