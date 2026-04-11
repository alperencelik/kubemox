package e2e_test

import (
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const pollInterval = 5 * time.Second

func keyFromObject(obj client.Object) client.ObjectKey {
	return client.ObjectKeyFromObject(obj)
}
