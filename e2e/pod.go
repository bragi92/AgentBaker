package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/yaml"
)

// Returns the name of a pod that's a member of the 'debug' daemonset, running on an aks-nodepool node.
func getDebugPodName(ctx context.Context, kube *Kubeclient) (string, error) {
	podList := corev1.PodList{}
	if err := kube.Dynamic.List(ctx, &podList, client.MatchingLabels{"app": "debug"}); err != nil {
		return "", fmt.Errorf("failed to list debug pod: %w", err)
	}

	if len(podList.Items) < 1 {
		return "", fmt.Errorf("failed to find debug pod, list by selector returned no results")
	}

	podName := podList.Items[0].Name
	return podName, nil
}

func applyPodManifest(ctx context.Context, namespace string, kube *Kubeclient, manifest string) error {
	var podObj corev1.Pod
	if err := yaml.Unmarshal([]byte(manifest), &podObj); err != nil {
		return fmt.Errorf("failed to unmarshal Pod manifest: %w", err)
	}

	podObj.Namespace = namespace

	desired := podObj.DeepCopy()
	_, err := controllerutil.CreateOrUpdate(ctx, kube.Dynamic, &podObj, func() error {
		podObj = *desired
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to apply Pod manifest: %w", err)
	}

	return nil
}

func ensurePod(ctx context.Context, t *testing.T, namespace string, kube *Kubeclient, podName, manifest string) error {
	if err := applyPodManifest(ctx, namespace, kube, manifest); err != nil {
		return fmt.Errorf("failed to ensure pod: %w", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		err := kube.Typed.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{})
		if err != nil {
			t.Logf("couldn't not delete pod %s: %v", podName, err)
		}
	})
	if err := waitUntilPodReady(ctx, kube, podName); err != nil {
		return fmt.Errorf("failed to wait for pod to be in running state: %w", err)
	}

	return nil
}
