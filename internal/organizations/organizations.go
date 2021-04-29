// Package organizations incapsulates all the functionality around Epinio organizations
// TODO: Consider moving this + the applications + the services packages under
// "models".
package organizations

import (
	"context"
	"fmt"

	"github.com/epinio/epinio/deployments"
	"github.com/epinio/epinio/helpers/kubernetes"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Organization struct {
	Name string
}

type GiteaInterface interface {
	CreateOrg(org string) error
}

func List(kubeClient *kubernetes.Cluster) ([]Organization, error) {
	listOptions := metav1.ListOptions{
		LabelSelector: kubernetes.EpinioOrgLabelKey + "=" + kubernetes.EpinioOrgLabelValue,
	}

	orgList, err := kubeClient.Kubectl.CoreV1().Namespaces().List(context.Background(), listOptions)
	if err != nil {
		return []Organization{}, err
	}

	result := []Organization{}
	for _, org := range orgList.Items {
		result = append(result, Organization{Name: org.ObjectMeta.Name})
	}

	return result, nil
}

func Exists(kubeClient *kubernetes.Cluster, lookupOrg string) (bool, error) {
	orgs, err := List(kubeClient)
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

func Create(kubeClient *kubernetes.Cluster, gitea GiteaInterface, org string) error {
	if _, err := kubeClient.Kubectl.CoreV1().Namespaces().Create(
		context.Background(),
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: org,
				Labels: map[string]string{
					kubernetes.EpinioOrgLabelKey:        kubernetes.EpinioOrgLabelValue,
					"quarks.cloudfoundry.org/monitored": "quarks-secret",
				},
			},
		},
		metav1.CreateOptions{},
	); err != nil {
		return err
	}

	// This secret is used as ImagePullSecrets for the application ServiceAccount
	// in order to allow the image to be pulled from the registry.
	if err := copySecret("registry-creds", deployments.TektonStagingNamespace, org, kubeClient); err != nil {
		return errors.Wrap(err, "failed to copy the registry credentials secret")
	}

	// Copy the CA certificate from the tekton-staging namespace.
	// This is needed to sign the self signed certificates on the application in
	// this new namespace.
	if err := copySecret("ca-cert", deployments.TektonStagingNamespace, org, kubeClient); err != nil {
		return errors.Wrap(err, "failed to copy the ca certificate")
	}

	if err := createServiceAccount(kubeClient, org); err != nil {
		return errors.Wrap(err, "failed to create a service account for apps")
	}

	return gitea.CreateOrg(org)
}

func copySecret(secretName, originOrg, targetOrg string, kubeClient *kubernetes.Cluster) error {
	fmt.Println("Will now copy " + secretName)
	secret, err := kubeClient.Kubectl.CoreV1().
		Secrets(originOrg).
		Get(context.Background(), secretName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	newSecret := secret.DeepCopy()
	newSecret.ObjectMeta.Namespace = targetOrg
	newSecret.ResourceVersion = ""
	newSecret.OwnerReferences = []metav1.OwnerReference{}
	fmt.Printf("newSecret = %+v\n", newSecret)

	_, err = kubeClient.Kubectl.CoreV1().Secrets(targetOrg).
		Create(context.Background(), newSecret, metav1.CreateOptions{})

	return err
}

func createServiceAccount(kubeClient *kubernetes.Cluster, targetOrg string) error {
	automountServiceAccountToken := false
	_, err := kubeClient.Kubectl.CoreV1().ServiceAccounts(targetOrg).Create(
		context.Background(),
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
