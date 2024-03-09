package kubernetes

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GetSecretValue(secretName, namespace, key string) (string, error) {
	secret, err := Clientset.CoreV1().Secrets(namespace).Get(context.Background(), secretName, metav1.GetOptions{})

	return string(secret.Data[key]), err
}
