package proxmox

import (
	"github.com/luthermonson/go-proxmox"
)

func GetTask(taskUPID string) *proxmox.Task {
	taskID := proxmox.UPID(
		taskUPID,
	)
	return proxmox.NewTask(taskID, Client)
}
