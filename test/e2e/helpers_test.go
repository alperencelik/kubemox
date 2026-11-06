package e2e_test

import (
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const pollInterval = 5 * time.Second

// e2eNamespace holds the namespaced resources the e2e suite creates.
const e2eNamespace = "default"

func keyFromObject(obj client.Object) client.ObjectKey {
	return client.ObjectKeyFromObject(obj)
}
