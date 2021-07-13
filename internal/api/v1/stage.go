package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"

	"github.com/epinio/epinio/internal/application"
	"github.com/epinio/epinio/internal/cli/clients/gitea"
	"github.com/epinio/epinio/internal/domain"
	"github.com/julienschmidt/httprouter"
	v1beta1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"github.com/tektoncd/pipeline/pkg/apis/resource/v1alpha1"
	"github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	k8s "k8s.io/client-go/kubernetes"

	"github.com/epinio/epinio/deployments"
	"github.com/epinio/epinio/helpers/kubernetes"
	"github.com/epinio/epinio/helpers/randstr"
	"github.com/epinio/epinio/helpers/tracelog"
	"github.com/epinio/epinio/internal/api/v1/models"
	"github.com/epinio/epinio/internal/auth"
)

const (
	DefaultInstances = int32(1)
)

type stageParam struct {
	models.AppRef
	Image       models.ImageRef
	Git         *models.GitRef
	Route       string
	Stage       models.StageRef
	Instances   int32
	Owner       metav1.OwnerReference
	Environment models.EnvVariableList
	Docker      string
}

// GitURL returns the git URL by combining the server with the org and name
func (app *stageParam) GitURL(server string) string {
	return fmt.Sprintf("%s/%s/%s", server, app.Org, app.Name)
}

// ImageURL returns the URL of the image, using the ImageID. The ImageURL is
// later used in app.yml.  Since the final commit is not known when the app.yml
// is written, we cannot use Repo.Revision
func (app *stageParam) ImageURL(server string) string {
	return fmt.Sprintf("%s/%s-%s", server, app.Name, app.Git.Revision)
}

// Stage will create a Tekton PipelineRun resource to stage and start the app
func (hc ApplicationsController) Stage(w http.ResponseWriter, r *http.Request) APIErrors {
	ctx := r.Context()
	log := tracelog.Logger(ctx)

	p := httprouter.ParamsFromContext(ctx)
	org := p.ByName("org")
	name := p.ByName("app")

	defer r.Body.Close()
	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return InternalError(err)
	}

	req := models.StageRequest{}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		return NewBadRequest("Failed to construct an Application from the request", err.Error())
	}

	if name != req.App.Name {
		return NewBadRequest("name parameter from URL does not match name param in body")
	}
	if org != req.App.Org {
		return NewBadRequest("org parameter from URL does not match org param in body")
	}

	if req.Instances != nil && *req.Instances < 0 {
		return NewBadRequest("instances param should be integer equal or greater than zero")
	}

	cluster, err := kubernetes.GetCluster(ctx)
	if err != nil {
		return InternalError(err, "failed to get access to a kube client")
	}

	// check application resource
	app, err := application.Get(ctx, cluster, req.App)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return AppIsNotKnown("cannot stage app, application resource is missing")
		}
		return InternalError(err, "failed to get the application resource")
	}

	log.Info("staging app", "org", org, "app", req)

	cs, err := versioned.NewForConfig(cluster.RestConfig)
	if err != nil {
		return InternalError(err, "failed to get access to a tekton client")
	}
	client := cs.TektonV1beta1().PipelineRuns(deployments.TektonStagingNamespace)

	uid, err := randstr.Hex16()
	if err != nil {
		return InternalError(err, "failed to generate a uid")
	}

	l, err := client.List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app.kubernetes.io/name=%s,app.kubernetes.io/part-of=%s", req.App.Name, req.App.Org),
	})
	if err != nil {
		return InternalError(err)
	}

	// assume that completed pipelineruns are from the past and have a CompletionTime
	for _, pr := range l.Items {
		if pr.Status.CompletionTime == nil {
			return NewBadRequest("pipelinerun for image ID still running")
		}
	}

	// find out the instances
	var instances int32
	if req.Instances != nil {
		instances = int32(*req.Instances)
	} else {
		instances, err = existingReplica(ctx, cluster.Kubectl, req.App)
		if err != nil {
			return InternalError(err)
		}
	}

	// determine runtime environment, if any
	env, err := application.Environment(ctx, cluster, req.App)
	if err != nil {
		return InternalError(err, "failed to access application runtime environment")
	}

	owner := metav1.OwnerReference{
		APIVersion: app.GetAPIVersion(),
		Kind:       app.GetKind(),
		Name:       app.GetName(),
		UID:        app.GetUID(),
	}
	params := stageParam{
		AppRef:      req.App,
		Git:         req.Git,
		Route:       req.Route,
		Instances:   instances,
		Owner:       owner,
		Environment: env,
	}

	mainDomain, err := domain.MainDomain(ctx)
	if err != nil {
		return InternalError(err)
	}

	if req.Docker == nil {
		var deploymentImageURL string
		registryURL := fmt.Sprintf("%s.%s/%s", deployments.RegistryDeploymentID, mainDomain, "apps")
		// If it's a local deployment the cert is self-signed so we use the NodePort
		// (without TLS) as the Deployment image. This way kube won't complain.
		if !strings.Contains(mainDomain, "omg.howdoi.website") {
			deploymentImageURL = registryURL
		} else {
			deploymentImageURL = gitea.LocalRegistry
		}

		pr := newPipelineRun(uid, params, mainDomain, registryURL, deploymentImageURL)
		o, err := client.Create(ctx, pr, metav1.CreateOptions{})
		if err != nil {
			return InternalError(err, fmt.Sprintf("failed to create pipeline run: %#v", o))
		}
	} else {

		obj, err := newDeployment(uid, params, mainDomain)
		if err != nil {
			return InternalError(err)
		}
		obj.SetOwnerReferences([]metav1.OwnerReference{owner})
		if _, err := cluster.Kubectl.AppsV1().Deployments(params.Org).Create(ctx, obj, metav1.CreateOptions{}); err != nil {
			return InternalError(err)
		}

		svc, err := newService(uid, params, mainDomain)
		if err != nil {
			return InternalError(err)
		}
		svc.SetOwnerReferences([]metav1.OwnerReference{owner})
		if _, err := cluster.Kubectl.CoreV1().Services(params.Org).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
			return InternalError(err)
		}

		ing, err := newIngress(uid, params, mainDomain)
		if err != nil {
			return InternalError(err)
		}
		ing.SetOwnerReferences([]metav1.OwnerReference{owner})
		if _, err := cluster.Kubectl.NetworkingV1().Ingresses(params.Org).Create(ctx, ing, metav1.CreateOptions{}); err != nil {
			return InternalError(err)
		}
	}

	err = auth.CreateCertificate(ctx, cluster, params.Name, params.Org, mainDomain, &owner)
	if err != nil {
		return InternalError(err)
	}

	log.Info("staged app", "org", org, "app", params.AppRef, "uid", uid)

	resp := models.StageResponse{Stage: models.NewStage(uid)}
	err = jsonResponse(w, resp)
	if err != nil {
		return InternalError(err)
	}

	return nil
}

func existingReplica(ctx context.Context, client *k8s.Clientset, app models.AppRef) (int32, error) {
	// if a deployment exists, use that deployment's replica count
	result, err := client.AppsV1().Deployments(app.Org).Get(ctx, app.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return DefaultInstances, nil
		}
		return 0, err
	}
	return *result.Spec.Replicas, nil
}

func newPipelineRun(uid string, app stageParam, mainDomain, registryURL, deploymentImageURL string) *v1beta1.PipelineRun {
	str := v1beta1.NewArrayOrString

	return &v1beta1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name: uid,
			Labels: map[string]string{
				"app.kubernetes.io/name":       app.Name,
				"app.kubernetes.io/part-of":    app.Org,
				models.EpinioStageIDLabel:      uid,
				"app.kubernetes.io/managed-by": "epinio",
				"app.kubernetes.io/component":  "staging",
			},
		},
		Spec: v1beta1.PipelineRunSpec{
			ServiceAccountName: "staging-triggers-admin",
			PipelineRef:        &v1beta1.PipelineRef{Name: "staging-pipeline"},
			Params: []v1beta1.Param{
				{Name: "APP_NAME", Value: *str(app.Name)},
				{Name: "ORG", Value: *str(app.Org)},
				{Name: "ROUTE", Value: *str(app.Route)},
				{Name: "INSTANCES", Value: *str(strconv.Itoa(int(app.Instances)))},
				{Name: "APP_IMAGE", Value: *str(app.ImageURL(registryURL))},
				{Name: "DEPLOYMENT_IMAGE", Value: *str(app.ImageURL(deploymentImageURL))},
				{Name: "STAGE_ID", Value: *str(uid)},

				{Name: "OWNER_APIVERSION", Value: *str(app.Owner.APIVersion)},
				{Name: "OWNER_NAME", Value: *str(app.Owner.Name)},
				{Name: "OWNER_KIND", Value: *str(app.Owner.Kind)},
				{Name: "OWNER_UID", Value: *str(string(app.Owner.UID))},
				{Name: "ENVIRONMENT", Value: *str(app.Environment.ToString(app.Name))},
			},
			Workspaces: []v1beta1.WorkspaceBinding{
				{
					Name: "source",
					VolumeClaimTemplate: &corev1.PersistentVolumeClaim{
						Spec: corev1.PersistentVolumeClaimSpec{
							AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
							Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
								corev1.ResourceName(corev1.ResourceStorage): resource.MustParse("1Gi"),
							}},
						},
					},
				},
			},
			Resources: []v1beta1.PipelineResourceBinding{
				{
					Name: "source-repo",
					ResourceSpec: &v1alpha1.PipelineResourceSpec{
						Type: v1alpha1.PipelineResourceTypeGit,
						Params: []v1alpha1.ResourceParam{
							{Name: "revision", Value: app.Git.Revision},
							{Name: "url", Value: app.Git.URL},
						},
					},
				},
			},
		},
	}
}

func newDeployment(uid string, app stageParam, mainDomain string) (*appsv1.Deployment, error) {
	data := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: "%[1]s"
  labels:
    app.kubernetes.io/name: "%[1]s"
    app.kubernetes.io/part-of: "%[2]s"
    app.kubernetes.io/component: application
    app.kubernetes.io/managed-by: epinio
spec:
  replicas: %[3]d
  selector:
    matchLabels:
      app.kubernetes.io/name: "%[1]s"
  template:
    metadata:
      labels:
        app.kubernetes.io/name: "%[1]s"
        epinio.suse.org/stage-id: "%[4]s"
        app.kubernetes.io/part-of: "%[2]s"
        app.kubernetes.io/component: application
        app.kubernetes.io/managed-by: epinio
      annotations:
        app.kubernetes.io/name: "%[1]s"
    spec:
      serviceAccountName: "%[2]s"
      automountServiceAccountToken: false
      containers:
      - name: "%[1]s"
        image: "%[5]s"
        ports:
        - containerPort: 8080
        env: %[6]s
	`,
		app.Name,
		app.Org,
		app.Instances,
		app.Stage.ID,
		app.Docker,
		app.Environment.ToString(app.Name))

	obj := &appsv1.Deployment{}
	dec := yaml.NewYAMLOrJSONDecoder(bytes.NewReader([]byte(data)), 1000)
	if err := dec.Decode(obj); err != nil {
		return nil, err
	}

	return obj, nil
}

func newService(uid string, app stageParam, mainDomain string) (*corev1.Service, error) {
	data := fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  annotations:
    kubernetes.io/ingress.class: traefik
    traefik.ingress.kubernetes.io/router.entrypoints: websecure
    traefik.ingress.kubernetes.io/router.tls: "true"
  labels:
    app.kubernetes.io/component: application
    app.kubernetes.io/managed-by: epinio
    app.kubernetes.io/name: %[1]s
    app.kubernetes.io/part-of: %[2]s
  name: %[1]s
  namespace: %[2]s
spec:
  ports:
  - port: 8080
    protocol: TCP
    targetPort: 8080
  selector:
    app.kubernetes.io/component: "application"
    app.kubernetes.io/name: "%[1]s"
  type: ClusterIP
	  `, app.Name, app.Org)

	obj := &corev1.Service{}
	dec := yaml.NewYAMLOrJSONDecoder(bytes.NewReader([]byte(data)), 1000)
	if err := dec.Decode(obj); err != nil {
		return nil, err
	}

	return obj, nil
}

func newIngress(uid string, app stageParam, mainDomain string) (*networkingv1.Ingress, error) {
	data := fmt.Sprintf(`apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  annotations:
    traefik.ingress.kubernetes.io/router.entrypoints: websecure
    traefik.ingress.kubernetes.io/router.tls: "true"
    kubernetes.io/ingress.class: traefik
  labels:
    app.kubernetes.io/component: application
    app.kubernetes.io/managed-by: epinio
    app.kubernetes.io/name: %[1]s
    app.kubernetes.io/part-of: %[2]s
  name: %[1]s
  namespace: %[2]s
spec:
  rules:
  - host: %[3]s
    http:
      paths:
      - backend:
    service:
      name: %[1]s
      port:
        number: 8080
  path: /
  pathType: ImplementationSpecific
  tls:
  - hosts:
    - %[3]s
    secretName: %[1]s-tls
	    `, app.Name, app.Org, app.Route)

	obj := &networkingv1.Ingress{}
	dec := yaml.NewYAMLOrJSONDecoder(bytes.NewReader([]byte(data)), 1000)
	if err := dec.Decode(obj); err != nil {
		return nil, err
	}

	return obj, nil
}
