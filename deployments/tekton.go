package deployments

import (
	"context"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/avast/retry-go"
	"github.com/epinio/epinio/helpers"
	"github.com/epinio/epinio/helpers/kubernetes"
	"github.com/epinio/epinio/helpers/termui"
	"github.com/epinio/epinio/internal/duration"
	"github.com/kyokomi/emoji"
	"github.com/pkg/errors"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	yaml2 "sigs.k8s.io/yaml"

	typedbatchv1 "k8s.io/client-go/kubernetes/typed/batch/v1"
)

type Tekton struct {
	Debug      bool
	Secrets    []string
	ConfigMaps []string
	Timeout    time.Duration
}

var _ kubernetes.Deployment = &Tekton{}

const (
	TektonDeploymentID            = "tekton"
	tektonNamespace               = "tekton"
	TektonStagingNamespace        = "tekton-staging"
	tektonPipelineReleaseYamlPath = "tekton/pipeline-v0.23.0.yaml"
	tektonAdminRoleYamlPath       = "tekton/admin-role.yaml"
	tektonStagingYamlPath         = "tekton/staging.yaml"
	tektonPipelineYamlPath        = "tekton/pipeline.yaml"
)

func (k *Tekton) ID() string {
	return TektonDeploymentID
}

func (k *Tekton) Backup(ctx context.Context, c *kubernetes.Cluster, ui *termui.UI, d string) error {
	return nil
}

func (k *Tekton) Restore(ctx context.Context, c *kubernetes.Cluster, ui *termui.UI, d string) error {
	return nil
}

func (k Tekton) Describe() string {
	return emoji.Sprintf(":cloud:Tekton pipeline: %s\n", tektonPipelineReleaseYamlPath)
}

// Delete removes Tekton from kubernetes cluster
func (k Tekton) Delete(ctx context.Context, c *kubernetes.Cluster, ui *termui.UI) error {
	ui.Note().KeeplineUnder(1).Msg("Removing Tekton...")

	existsAndOwned, err := c.NamespaceExistsAndOwned(ctx, TektonStagingNamespace)
	if err != nil {
		return errors.Wrapf(err, "failed to check if namespace '%s' is owned or not", TektonStagingNamespace)
	}
	if !existsAndOwned {
		ui.Exclamation().Msg("Skipping Tekton staging because namespace either doesn't exist or not owned by Epinio")
		return nil
	}

	if out, err := helpers.KubectlDeleteEmbeddedYaml(tektonAdminRoleYamlPath, true); err != nil {
		return errors.Wrap(err, fmt.Sprintf("Deleting %s failed:\n%s", tektonAdminRoleYamlPath, out))
	}

	message := "Deleting Tekton staging namespace " + TektonStagingNamespace
	_, err = helpers.WaitForCommandCompletion(ui, message,
		func() (string, error) {
			return "", c.DeleteNamespace(ctx, TektonStagingNamespace)
		},
	)
	if err != nil {
		return errors.Wrapf(err, "Failed deleting namespace %s", TektonStagingNamespace)
	}

	err = c.WaitForNamespaceMissing(ctx, ui, TektonStagingNamespace, k.Timeout)
	if err != nil {
		return errors.Wrapf(err, "Failed waiting for namespace %s to be deleted", TektonStagingNamespace)
	}

	existsAndOwned, err = c.NamespaceExistsAndOwned(ctx, tektonNamespace)
	if err != nil {
		return errors.Wrapf(err, "failed to check if namespace '%s' is owned or not", tektonNamespace)
	}
	if !existsAndOwned {
		ui.Exclamation().Msg("Skipping Tekton because namespace either doesn't exist or not owned by Epinio")
		return nil
	}

	if out, err := helpers.KubectlDeleteEmbeddedYaml(tektonPipelineReleaseYamlPath, true); err != nil {
		return errors.Wrap(err, fmt.Sprintf("Deleting %s failed:\n%s", tektonPipelineReleaseYamlPath, out))
	}

	message = "Deleting Tekton namespace " + tektonNamespace
	_, err = helpers.WaitForCommandCompletion(ui, message,
		func() (string, error) {
			return "", c.DeleteNamespace(ctx, tektonNamespace)
		},
	)
	if err != nil {
		return errors.Wrapf(err, "Failed deleting namespace %s", tektonNamespace)
	}

	err = c.WaitForNamespaceMissing(ctx, ui, tektonNamespace, k.Timeout)
	if err != nil {
		return errors.Wrap(err, "failed to delete namespace")
	}

	ui.Success().Msg("Tekton removed")

	return nil
}

func (k Tekton) apply(ctx context.Context, c *kubernetes.Cluster, ui *termui.UI, options kubernetes.InstallationOptions, upgrade bool) error {
	if err := c.CreateNamespace(ctx, tektonNamespace, map[string]string{
		kubernetes.EpinioDeploymentLabelKey: kubernetes.EpinioDeploymentLabelValue,
	}, map[string]string{"linkerd.io/inject": "enabled"}); err != nil {
		return err
	}

	if err := c.CreateNamespace(ctx, TektonStagingNamespace, map[string]string{
		kubernetes.EpinioDeploymentLabelKey: kubernetes.EpinioDeploymentLabelValue,
		"cert-manager-tls":                  RegistryDeploymentID,
	}, map[string]string{"linkerd.io/inject": "enabled"}); err != nil {
		return err
	}

	if out, err := helpers.KubectlApplyEmbeddedYaml(tektonPipelineReleaseYamlPath); err != nil {
		return errors.Wrap(err, fmt.Sprintf("Installing %s failed:\n%s", tektonPipelineReleaseYamlPath, out))
	}
	if out, err := helpers.KubectlApplyEmbeddedYaml(tektonAdminRoleYamlPath); err != nil {
		return errors.Wrap(err, fmt.Sprintf("Installing %s failed:\n%s", tektonAdminRoleYamlPath, out))
	}

	kTimeout := strconv.Itoa(int(k.Timeout.Seconds()))

	err := c.WaitUntilPodBySelectorExist(ctx, ui, tektonNamespace, "app=tekton-pipelines-webhook", k.Timeout)
	if err != nil {
		return errors.Wrap(err, "failed waiting tekton pipelines webhook pod to exist")
	}
	err = c.WaitForPodBySelectorRunning(ctx, ui, tektonNamespace, "app=tekton-pipelines-webhook", k.Timeout)
	if err != nil {
		return errors.Wrap(err, "failed waiting tekton pipelines webhook pod to be running")
	}

	for _, crd := range []string{
		"clustertasks.tekton.dev",
		"conditions.tekton.dev",
		"pipelineresources.tekton.dev",
		"pipelineruns.tekton.dev",
		"pipelines.tekton.dev",
		"runs.tekton.dev",
		"taskruns.tekton.dev",
		"tasks.tekton.dev",
	} {
		message := fmt.Sprintf("Establish CRD %s", crd)
		out, err := helpers.WaitForCommandCompletion(ui, message,
			func() (string, error) {
				return helpers.Kubectl("wait --for=condition=established --timeout=" + kTimeout + "s crd/" + crd)
			},
		)
		if err != nil {
			return errors.Wrap(err, fmt.Sprintf("%s failed:\n%s", message, out))
		}
	}

	message := "Installing staging pipelines"
	// Workaround for tekton webhook service not working, despite pod and deployment being ready
	err = retry.Do(
		func() error {
			out, err := helpers.WaitForCommandCompletion(ui, message,
				func() (string, error) {
					return helpers.KubectlApplyEmbeddedYaml(tektonPipelineYamlPath)
				},
			)
			if err != nil {
				return errors.Wrap(err, fmt.Sprintf("%s failed:\n%s", message, out))
			}
			return nil
		},
		retry.RetryIf(func(err error) bool {
			return strings.Contains(err.Error(), "connection refused") ||
				strings.Contains(err.Error(), "EOF")
		}),
		retry.OnRetry(func(n uint, err error) {
			ui.Note().Msgf("retrying to apply %s", tektonPipelineYamlPath)
		}),
		retry.Delay(5*time.Second),
	)
	if err != nil {
		return err
	}

	domain, err := options.GetString("system_domain", TektonDeploymentID)
	if err != nil {
		return errors.Wrap(err, "Couldn't get system_domain option")
	}

	if err := k.createGiteaCredsSecret(ctx, c); err != nil {
		return err
	}
	if err := k.createClusterRegistryCredsSecret(ctx, c, domain); err != nil {
		return err
	}

	message = fmt.Sprintf("Checking registry certificates in %s", RegistryDeploymentID)
	out, err := helpers.WaitForCommandCompletion(ui, message,
		func() (string, error) {
			out, err := helpers.ExecToSuccessWithTimeout(
				func() (string, error) {
					out, err := helpers.Kubectl(fmt.Sprintf(`get secret -n %s %s -o 'jsonpath={.data.tls\.crt}'`, RegistryDeploymentID, RegistryCertSecret))
					if err != nil {
						return "", err
					}

					if out == "" {
						return "", errors.New("secret is not filled")
					}
					return out, nil
				}, k.Timeout, duration.PollInterval())
			return out, err
		},
	)
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("%s failed:\n%s", message, out))
	}

	message = "Applying tekton staging resources"

	out, err = helpers.WaitForCommandCompletion(ui, message,
		func() (string, error) {
			return "", applyTektonStaging(ctx, c, domain)
		},
	)
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("%s failed:\n%s", message, out))
	}

	ui.Success().Msg("Tekton deployed")

	return nil
}

func (k Tekton) GetVersion() string {
	return fmt.Sprintf("pipelines: %s", tektonPipelineReleaseYamlPath)
}

func (k Tekton) Deploy(ctx context.Context, c *kubernetes.Cluster, ui *termui.UI, options kubernetes.InstallationOptions) error {

	_, err := c.Kubectl.CoreV1().Namespaces().Get(
		ctx,
		TektonDeploymentID,
		metav1.GetOptions{},
	)
	if err == nil {
		return errors.New("Namespace " + TektonDeploymentID + " present already")
	}

	ui.Note().KeeplineUnder(1).Msg("Deploying Tekton...")

	err = k.apply(ctx, c, ui, options, false)
	if err != nil {
		return err
	}

	s := ui.Progress("Warming up cluster with builder image")
	err = k.warmupBuilder(ctx, c)
	if err != nil {
		return err
	}
	s.Stop()

	return nil
}

func (k Tekton) Upgrade(ctx context.Context, c *kubernetes.Cluster, ui *termui.UI, options kubernetes.InstallationOptions) error {
	_, err := c.Kubectl.CoreV1().Namespaces().Get(
		ctx,
		TektonDeploymentID,
		metav1.GetOptions{},
	)
	if err != nil {
		return errors.New("Namespace " + TektonDeploymentID + " not present")
	}

	ui.Note().Msg("Upgrading Tekton...")

	return k.apply(ctx, c, ui, options, true)
}

// The equivalent of:
// kubectl get secret -n tekton-staging epinio-registry-tls -o json | jq -r '.["data"]["ca.crt"]' | base64 -d | openssl x509 -hash -noout
// written in golang.
func getRegistryCAHash(ctx context.Context, c *kubernetes.Cluster) (string, error) {
	secret, err := c.Kubectl.CoreV1().Secrets(TektonStagingNamespace).
		Get(ctx, RegistryCertSecret, metav1.GetOptions{})
	if err != nil {
		return "", err
	}

	// cert-manager doesn't add the CA for ACME certificates:
	// https://github.com/jetstack/cert-manager/issues/2111
	if _, found := secret.Data["ca.crt"]; !found {
		return "", nil
	}

	hash, err := GenerateHash(secret.Data["ca.crt"])
	if err != nil {
		return "", err
	}

	return hash, nil
}

func applyTektonStaging(ctx context.Context, c *kubernetes.Cluster, domain string) error {
	yamlPathOnDisk, err := helpers.ExtractFile(tektonStagingYamlPath)
	if err != nil {
		return errors.New("Failed to extract embedded file: " + tektonStagingYamlPath + " - " + err.Error())
	}
	defer os.Remove(yamlPathOnDisk)

	fileContents, err := ioutil.ReadFile(yamlPathOnDisk)
	if err != nil {
		return err
	}

	tektonTask := &v1beta1.Task{}
	err = yaml2.Unmarshal(fileContents, tektonTask, func(opt *json.Decoder) *json.Decoder {
		opt.UseNumber()
		return opt
	})
	if err != nil {
		return errors.Wrapf(err, "failed to unmarshal task %s", string(fileContents))
	}

	// TODO this workaround is only needed for untrusted certs.
	//  Once we can reach Tekton via linkerd, blocked by
	//  https://github.com/tektoncd/catalog/issues/757, we can remove the
	//  workaround.

	// Add volume and volume mount of registry-certs for local deployment
	// since tekton should trust the registry-certs.
	caHash, err := getRegistryCAHash(ctx, c)
	if err != nil {
		return errors.Wrapf(err, "Failed to get registry CA from %s namespace", TektonStagingNamespace)
	}

	if caHash != "" {
		volume := corev1.Volume{
			Name: "registry-certs",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: RegistryCertSecret,
				},
			},
		}
		tektonTask.Spec.Volumes = append(tektonTask.Spec.Volumes, volume)

		volumeMount := corev1.VolumeMount{
			Name:      "registry-certs",
			MountPath: fmt.Sprintf("%s/%s", "/etc/ssl/certs", caHash),
			SubPath:   "ca.crt",
			ReadOnly:  true,
		}
		for stepIndex, step := range tektonTask.Spec.Steps {
			if step.Name == "create" {
				tektonTask.Spec.Steps[stepIndex].VolumeMounts = append(tektonTask.Spec.Steps[stepIndex].VolumeMounts, volumeMount)
				break
			}
		}
	}

	clientSet, err := versioned.NewForConfig(c.RestConfig)
	if err != nil {
		return errors.Wrapf(err, "failed getting tekton Task clientSet")
	}

	_, err = clientSet.TektonV1beta1().Tasks(TektonStagingNamespace).Create(ctx, tektonTask, metav1.CreateOptions{})
	if err != nil {
		return errors.Wrapf(err, "failed creating tekton Task")
	}

	return nil
}

func (k Tekton) createGiteaCredsSecret(ctx context.Context, c *kubernetes.Cluster) error {
	// See internal/cli/clients/gitea/gitea.go, func
	// `getGiteaCredentials` for where the cli retrieves the
	// information for its own gitea client.
	//
	// See deployments/gitea.go func `apply` where `install`
	// configures gitea for the same credentials.

	giteaAuth, err := GiteaInstallAuth()
	if err != nil {
		return err
	}

	_, err = c.Kubectl.CoreV1().Secrets(TektonStagingNamespace).Create(ctx,
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gitea-creds",
				Annotations: map[string]string{
					"tekton.dev/git-0": GiteaURL,
				},
			},
			StringData: map[string]string{
				"username": giteaAuth.Username,
				"password": giteaAuth.Password,
			},
			Type: "kubernetes.io/basic-auth",
		}, metav1.CreateOptions{})

	if err != nil {
		return err
	}
	return nil
}

func (k Tekton) createClusterRegistryCredsSecret(ctx context.Context, c *kubernetes.Cluster, domain string) error {
	// Generate random credentials
	registryAuth, err := RegistryInstallAuth()
	if err != nil {
		return err
	}

	encodedCredentials := base64.StdEncoding.EncodeToString([]byte(
		registryAuth.Username + ":" + registryAuth.Password))
	jsonFull := fmt.Sprintf(`"auth":"%s","username":"%s","password":"%s"`,
		encodedCredentials, registryAuth.Username, registryAuth.Password)
	jsonPart := fmt.Sprintf(`"username":"%s","password":"%s"`,
		registryAuth.Username, registryAuth.Password)

	// TODO: Are all of these really used? We need tekton to be able to access
	// the registry and also kubernetes (when we deploy our app deployments)
	auths := fmt.Sprintf(`{ "auths": {
		"https://127.0.0.1:30500":{%s},
		"%s":{%s} } }`,
		jsonFull, fmt.Sprintf("%s.%s", RegistryDeploymentID, domain), jsonPart)
	// The relevant place in the registry is `deployments/registry.go`, func `apply`, see (**).

	_, err = c.Kubectl.CoreV1().Secrets(TektonStagingNamespace).Create(ctx,
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "registry-creds",
			},
			StringData: map[string]string{
				".dockerconfigjson": auths,
			},
			Type: "kubernetes.io/dockerconfigjson",
		}, metav1.CreateOptions{})

	if err != nil {
		return err
	}
	return nil
}

// This function creates a dummy Job using the buildpack builder image
// in order to avoid pulling it the first time we an application is staged.
// TODO: This doesn't work in a multi-node cluster because it will only pull
// the image on one node. Maybe we could use a dummy daemonset for that.
// This does not help with custom builder images.
func (k Tekton) warmupBuilder(ctx context.Context, c *kubernetes.Cluster) error {
	client, err := typedbatchv1.NewForConfig(c.RestConfig)
	if err != nil {
		return err
	}

	jobName := "buildpack-builder-warmup"
	var backoffLimit = int32(1)
	if _, err = client.Jobs(TektonStagingNamespace).Create(
		ctx,
		&batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name: jobName,
				Labels: map[string]string{
					kubernetes.EpinioDeploymentLabelKey: kubernetes.EpinioDeploymentLabelValue,
				},
			},
			Spec: batchv1.JobSpec{
				BackoffLimit: &backoffLimit,
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Name: jobName,
						Labels: map[string]string{
							kubernetes.EpinioDeploymentLabelKey: kubernetes.EpinioDeploymentLabelValue,
						},
						Annotations: map[string]string{
							"linkerd.io/inject": "disabled", // Let it finish
						},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:    "warmup",
								Image:   "paketobuildpacks/builder:full",
								Command: []string{"/bin/ls"},
							}},
						RestartPolicy: "Never",
					}}}},
		metav1.CreateOptions{},
	); err != nil {
		return err
	}

	return c.WaitForJobCompleted(ctx, TektonStagingNamespace, jobName, duration.ToWarmupJobReady())
}

// -----------------------------------------------------------------------------------

func GenerateHash(certRaw []byte) (string, error) {
	cert, err := decodeOneCert(certRaw)
	if err != nil {
		return "", fmt.Errorf("failed to decode certificate\n%w", err)
	}

	hash, err := SubjectNameHash(cert)
	if err != nil {
		return "", fmt.Errorf("failed compute subject name hash for cert\n%w", err)
	}

	name := fmt.Sprintf("%08x", hash)
	return name, nil
}

// -----------------------------------------------------------------------------------
// See gh:paketo-buildpacks/ca-certificates (cacerts/certs.go) for the original code.

func decodeOneCert(raw []byte) (*x509.Certificate, error) {
	block, rest := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("failed find PEM data")
	}
	extra, rest := pem.Decode(rest)
	if extra != nil {
		return nil, errors.New("found multiple PEM blocks, expected exactly one")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certficate\n%w", err)
	}
	return cert, nil
}

// SubjectNameHash is a reimplementation of the X509_subject_name_hash
// in openssl. It computes the SHA-1 of the canonical encoding of the
// certificate's subject name and returns the 32-bit integer
// represented by the first four bytes of the hash using little-endian
// byte order.
func SubjectNameHash(cert *x509.Certificate) (uint32, error) {
	name, err := CanonicalName(cert.RawSubject)
	if err != nil {
		return 0, fmt.Errorf("failed to compute canonical subject name\n%w", err)
	}
	hasher := sha1.New()
	_, err = hasher.Write(name)
	if err != nil {
		return 0, fmt.Errorf("failed to compute sha1sum of canonical subject name\n%w", err)
	}
	sum := hasher.Sum(nil)
	return binary.LittleEndian.Uint32(sum[:4]), nil
}

// canonicalSET holds a of canonicalATVs. Suffix SET ensures it is
// marshaled as a set rather than a sequence by asn1.Marshal.
type canonicalSET []canonicalATV

// canonicalATV is similar to pkix.AttributeTypeAndValue but includes
// tag to ensure all values are marshaled as ASN.1, UTF8String values
type canonicalATV struct {
	Type  asn1.ObjectIdentifier
	Value string `asn1:"utf8"`
}

// CanonicalName accepts a DER encoded subject name and returns a
// "Canonical Encoding" matching that returned by the x509_name_canon
// function in openssl. All string values are transformed with
// CanonicalString and UTF8 encoded and the leading SEQ header is
// removed.
//
// For more information see
// https://stackoverflow.com/questions/34095440/hash-algorithm-for-certificate-crl-directory.
func CanonicalName(name []byte) ([]byte, error) {
	var origSeq pkix.RDNSequence
	_, err := asn1.Unmarshal(name, &origSeq)
	if err != nil {
		return nil, fmt.Errorf("failed to parse subject name\n%w", err)
	}
	var result []byte
	for _, origSet := range origSeq {
		var canonSet canonicalSET
		for _, origATV := range origSet {
			origVal, ok := origATV.Value.(string)
			if !ok {
				return nil, errors.New("got unexpected non-string value")
			}
			canonSet = append(canonSet, canonicalATV{
				Type:  origATV.Type,
				Value: CanonicalString(origVal),
			})
		}
		setBytes, err := asn1.Marshal(canonSet)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal canonical name\n%w", err)
		}
		result = append(result, setBytes...)
	}
	return result, nil
}

// CanonicalString transforms the given string. All leading and
// trailing whitespace is trimmed where whitespace is defined as a
// space, formfeed, tab, newline, carriage return, or vertical tab
// character. Any remaining sequence of one or more consecutive
// whitespace characters in replaced with a single ' '.
//
// This is a reimplementation of the asn1_string_canon in openssl
func CanonicalString(s string) string {
	s = strings.TrimLeft(s, " \f\t\n\n\v")
	s = strings.TrimRight(s, " \f\t\n\n\v")
	s = strings.ToLower(s)
	return string(regexp.MustCompile(`[[:space:]]+`).ReplaceAll([]byte(s), []byte(" ")))
}
