package v1alpha1

const (

	// ReconcileModeAnnotation is the annotation key for the reconcile mode
	// ReconcileMode is the mode of the reconciliation it could be Normal, WatchOnly, EnsureExists, Disable, DryRun (to be implemented)
	ReconcileModeAnnotation   = "proxmox.alperen.cloud/reconcile-mode"
	ReconcileModeNormal       = "Normal"
	ReconcileModeWatchOnly    = "WatchOnly"
	ReconcileModeEnsureExists = "EnsureExists"
	ReconcileModeDisable      = "Disable"
	ReconcileModeDryRun       = "DryRun"
)
