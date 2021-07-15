// Package organizations incapsulates all the functionality around Epinio organizations
// TODO: Consider moving this + the applications + the services packages under
// "models".
package organizations

import (
	"context"

	"github.com/epinio/epinio/deployments"
	"github.com/epinio/epinio/helpers/kubernetes"
	"github.com/epinio/epinio/helpers/tracelog"
	"github.com/epinio/epinio/internal/duration"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Organization struct {
	Name string
}

type GiteaInterface interface {
	CreateOrg(org string) error
	DeleteOrg(org string) error
}

func List(ctx context.Context, kubeClient *kubernetes.Cluster) ([]Organization, error) {
	listOptions := metav1.ListOptions{
		LabelSelector: kubernetes.EpinioOrgLabelKey + "=" + kubernetes.EpinioOrgLabelValue,
	}

	orgList, err := kubeClient.Kubectl.CoreV1().Namespaces().List(ctx, listOptions)
	if err != nil {
		return []Organization{}, err
	}

	result := []Organization{}
	for _, org := range orgList.Items {
		result = append(result, Organization{Name: org.ObjectMeta.Name})
	}

	return result, nil
}

func Exists(ctx context.Context, kubeClient *kubernetes.Cluster, lookupOrg string) (bool, error) {
	orgs, err := List(ctx, kubeClient)
	if err != nil {
		return false, err
	}
	for _, org := range orgs {
		if org.Name == lookupOrg {
			return true, nil
		}
	}

	return false, nil
}

func Create(ctx context.Context, kubeClient *kubernetes.Cluster, gitea GiteaInterface, org string) error {
	if _, err := kubeClient.Kubectl.CoreV1().Namespaces().Create(
		ctx,
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: org,
				Labels: map[string]string{
					kubernetes.EpinioOrgLabelKey: kubernetes.EpinioOrgLabelValue,
				},
				Annotations: map[string]string{
					"linkerd.io/inject": "enabled",
				},
			},
		},
		metav1.CreateOptions{},
	); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return errors.Errorf("Org '%s' name cannot be used. Please try another name", org)
		}
		return err
	}

	// This secret is used as ImagePullSecrets for the application ServiceAccount
	// in order to allow the image to be pulled from the registry.
	if err := copySecret(ctx, "registry-creds", deployments.TektonStagingNamespace, org, kubeClient); err != nil {
		return errors.Wrap(err, "failed to copy the registry credentials secret")
	}

	if err := createServiceAccount(ctx, kubeClient, org); err != nil {
		return errors.Wrap(err, "failed to create a service account for apps")
	}

	return gitea.CreateOrg(org)
}

func Delete(ctx context.Context, kubeClient *kubernetes.Cluster, gitea GiteaInterface, org string) error {
	err := kubeClient.Kubectl.CoreV1().Namespaces().Delete(ctx, org, metav1.DeleteOptions{})
	if err != nil {
		return err
	}

	err = kubeClient.WaitForNamespaceMissing(ctx, nil, org, duration.ToOrgDeletion())
	if err != nil {
		return err
	}

	return gitea.DeleteOrg(org)
}

func copySecret(ctx context.Context, secretName, originOrg, targetOrg string, kubeClient *kubernetes.Cluster) error {
	log := tracelog.Logger(ctx)
	log.V(1).Info("will now copy secret", "name", secretName)
	secret, err := kubeClient.Kubectl.CoreV1().
		Secrets(originOrg).
		Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	newSecret := secret.DeepCopy()
	newSecret.ObjectMeta.Namespace = targetOrg
	newSecret.ResourceVersion = ""
	newSecret.OwnerReferences = []metav1.OwnerReference{}
	log.V(2).Info("newSecret", "data", newSecret)

	_, err = kubeClient.Kubectl.CoreV1().Secrets(targetOrg).
		Create(ctx, newSecret, metav1.CreateOptions{})

	return err
}

func createServiceAccount(ctx context.Context, kubeClient *kubernetes.Cluster, targetOrg string) error {
	automountServiceAccountToken := true
	_, err := kubeClient.Kubectl.CoreV1().ServiceAccounts(targetOrg).Create(
		ctx,
		&corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name: targetOrg,
			},
			ImagePullSecrets: []corev1.LocalObjectReference{
				{Name: "registry-creds"},
			},
			AutomountServiceAccountToken: &automountServiceAccountToken,
		}, metav1.CreateOptions{})

	return err
}
