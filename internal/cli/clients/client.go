package clients

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"code.gitea.io/sdk/gitea"
	"github.com/epinio/epinio/deployments"
	"github.com/epinio/epinio/helpers/kubernetes"
	kubeconfig "github.com/epinio/epinio/helpers/kubernetes/config"
	"github.com/epinio/epinio/helpers/kubernetes/tailer"
	"github.com/epinio/epinio/helpers/termui"
	"github.com/epinio/epinio/internal/api/v1/models"
	"github.com/epinio/epinio/internal/application"
	"github.com/epinio/epinio/internal/cli/config"
	"github.com/epinio/epinio/internal/duration"
	"github.com/epinio/epinio/internal/services"
	"github.com/go-logr/logr"
	"github.com/otiai10/copy"
	"github.com/pkg/errors"
	"github.com/spf13/pflag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/client-go/dynamic"
)

var (
	// HookSecret should be generated
	// TODO: generate this and put it in a secret
	HookSecret = "74tZTBHkhjMT5Klj6Ik6PqmM"

	// StagingEventListenerURL should not exist
	// TODO: detect this based on namespaces and services
	StagingEventListenerURL = "http://el-staging-listener." + deployments.TektonStagingNamespace + ":8080"
)

// EpinioClient provides functionality for talking to a
// Epinio installation on Kubernetes
type EpinioClient struct {
	GiteaClient *GiteaClient
	KubeClient  *kubernetes.Cluster
	Config      *config.Config
	Log         logr.Logger
	ui          *termui.UI
	serverURL   string
}

func NewEpinioClient(flags *pflag.FlagSet) (*EpinioClient, error) {
	configConfig, err := config.Load()
	if err != nil {
		return nil, err
	}

	cluster, err := kubernetes.GetCluster()
	if err != nil {
		return nil, err
	}

	client, err := GetGiteaClient()

	if err != nil {
		return nil, err
	}

	uiUI := termui.NewUI()
	epClient, err := GetEpinioAPIClient()
	if err != nil {
		return nil, err
	}
	serverURL := epClient.URL

	logger := kubeconfig.NewClientLogger()
	epinioClient := &EpinioClient{
		GiteaClient: client,
		KubeClient:  cluster,
		ui:          uiUI,
		Config:      configConfig,
		Log:         logger,
		serverURL:   serverURL,
	}
	return epinioClient, nil
}

// ServicePlans gets all service classes in the cluster, for the
// specified class
func (c *EpinioClient) ServicePlans(serviceClassName string) error {
	log := c.Log.WithName("ServicePlans").WithValues("ServiceClass", serviceClassName)
	log.Info("start")
	defer log.Info("return")
	details := log.V(1) // NOTE: Increment of level, not absolute.

	c.ui.Note().
		Msg("Listing service plans")

	// todo: sort service plans by name before display
	jsonResponse, err := c.curl(fmt.Sprintf("api/v1/serviceclasses/%s/serviceplans", serviceClassName), "GET", "")
	if err != nil {
		return err
	}
	var servicePlans services.ServicePlanList
	if err := json.Unmarshal(jsonResponse, &servicePlans); err != nil {
		return err
	}

	details.Info("list service plans")

	msg := c.ui.Success().WithTable("Plan", "Free", "Description")
	for _, sp := range servicePlans {
		var isFree string
		if sp.Free {
			isFree = "yes"
		} else {
			isFree = "no"
		}
		msg = msg.WithTableRow(sp.Name, isFree, sp.Description)
	}
	msg.Msg("Epinio Service Plans:")

	return nil
}

// ServicePlanMatching gets all service plans in the cluster, for the
// specified class, and the given prefix
func (c *EpinioClient) ServicePlanMatching(serviceClassName, prefix string) []string {
	log := c.Log.WithName("ServicePlans").WithValues("PrefixToMatch", prefix)
	log.Info("start")
	defer log.Info("return")
	details := log.V(1) // NOTE: Increment of level, not absolute.

	result := []string{}

	serviceClass, err := services.ClassLookup(c.KubeClient, serviceClassName)
	if err != nil {
		return result
	}

	servicePlans, err := serviceClass.ListPlans()
	if err != nil {
		return result
	}

	for _, sp := range servicePlans {
		details.Info("Found", "Name", sp.Name)
		if strings.HasPrefix(sp.Name, prefix) {
			details.Info("Matched", "Name", sp.Name)
			result = append(result, sp.Name)
		}
	}

	return result
}

// ServiceClassMatching returns all service classes in the cluster which have the specified prefix in their name
func (c *EpinioClient) ServiceClassMatching(prefix string) []string {
	log := c.Log.WithName("ServiceClasses").WithValues("PrefixToMatch", prefix)
	log.Info("start")
	defer log.Info("return")
	details := log.V(1) // NOTE: Increment of level, not absolute.

	result := []string{}

	serviceClasses, err := services.ListClasses(c.KubeClient)
	if err != nil {
		details.Info("Error", err)
		return result
	}

	details.Info("Filtering")
	for _, sc := range serviceClasses {
		details.Info("Found", "Name", sc.Name)
		if strings.HasPrefix(sc.Name, prefix) {
			details.Info("Matched", "Name", sc.Name)
			result = append(result, sc.Name)
		}
	}

	return result
}

// ServiceClasses gets all service classes in the cluster
func (c *EpinioClient) ServiceClasses() error {
	log := c.Log.WithName("ServiceClasses")
	log.Info("start")
	defer log.Info("return")
	details := log.V(1) // NOTE: Increment of level, not absolute.

	c.ui.Note().
		Msg("Listing service classes")

	jsonResponse, err := c.curl("api/v1/serviceclasses", "GET", "")
	if err != nil {
		return err
	}
	var serviceClasses services.ServiceClassList
	if err := json.Unmarshal(jsonResponse, &serviceClasses); err != nil {
		return err
	}

	// todo: sort service classes by name before display
	details.Info("list service classes")

	msg := c.ui.Success().WithTable("Name", "Description", "Broker")
	for _, sc := range serviceClasses {
		msg = msg.WithTableRow(sc.Name, sc.Description, sc.Broker)
	}
	msg.Msg("Epinio Service Classes:")

	return nil
}

// Services gets all Epinio services in the targeted org
func (c *EpinioClient) Services() error {
	log := c.Log.WithName("Services").WithValues("Organization", c.Config.Org)
	log.Info("start")
	defer log.Info("return")
	details := log.V(1) // NOTE: Increment of level, not absolute.

	c.ui.Note().
		WithStringValue("Organization", c.Config.Org).
		Msg("Listing services")

	details.Info("list applications")

	jsonResponse, err := c.curl(fmt.Sprintf("api/v1/orgs/%s/services/", c.Config.Org), "GET", "")
	if err != nil {
		return err
	}
	var response models.ServiceResponseList
	if err := json.Unmarshal(jsonResponse, &response); err != nil {
		return err
	}

	// todo: sort services by name before display
	details.Info("list services")

	msg := c.ui.Success().WithTable("Name", "Applications")

	// todo: sort services by name before display
	details.Info("list services")
	for _, service := range response {
		msg = msg.WithTableRow(service.Name, strings.Join(service.BoundApps, ", "))
	}
	msg.Msg("Epinio Services:")

	return nil
}

// ServiceMatching returns all Epinio services having the specified prefix
// in their name.
func (c *EpinioClient) ServiceMatching(prefix string) []string {
	log := c.Log.WithName("ServiceMatching").WithValues("PrefixToMatch", prefix)
	log.Info("start")
	defer log.Info("return")
	details := log.V(1) // NOTE: Increment of level, not absolute.

	result := []string{}

	orgServices, err := services.List(c.KubeClient, c.Config.Org)
	if err != nil {
		return result
	}

	for _, s := range orgServices {
		service := s.Name()
		details.Info("Found", "Name", service)
		if strings.HasPrefix(service, prefix) {
			details.Info("Matched", "Name", service)
			result = append(result, service)
		}
	}

	return result
}

// BindService attaches a service specified by name to the named application,
// both in the targeted organization.
func (c *EpinioClient) BindService(serviceName, appName string) error {
	log := c.Log.WithName("Bind Service To Application").
		WithValues("Name", serviceName, "Application", appName, "Organization", c.Config.Org)
	log.Info("start")
	defer log.Info("return")

	c.ui.Note().
		WithStringValue("Service", serviceName).
		WithStringValue("Application", appName).
		WithStringValue("Organization", c.Config.Org).
		Msg("Bind Service")

	request := models.BindRequest{
		Name: serviceName,
	}

	js, err := json.Marshal(request)
	if err != nil {
		return err
	}

	_, err = c.curl(fmt.Sprintf("api/v1/orgs/%s/applications/%s/servicebindings", c.Config.Org, appName),
		"POST", string(js))
	if err != nil {
		return err
	}

	c.ui.Success().
		WithStringValue("Service", serviceName).
		WithStringValue("Application", appName).
		WithStringValue("Organization", c.Config.Org).
		Msg("Service Bound to Application.")
	return nil
}

// UnbindService detaches the service specified by name from the named
// application, both in the targeted organization.
func (c *EpinioClient) UnbindService(serviceName, appName string) error {
	log := c.Log.WithName("Unbind Service").
		WithValues("Name", serviceName, "Application", appName, "Organization", c.Config.Org)
	log.Info("start")
	defer log.Info("return")

	c.ui.Note().
		WithStringValue("Service", serviceName).
		WithStringValue("Application", appName).
		WithStringValue("Organization", c.Config.Org).
		Msg("Unbind Service from Application")

	_, err := c.curl(fmt.Sprintf("api/v1/orgs/%s/applications/%s/servicebindings/%s",
		c.Config.Org, appName, serviceName), "DELETE", "")
	if err != nil {
		return err
	}

	c.ui.Success().
		WithStringValue("Service", serviceName).
		WithStringValue("Application", appName).
		WithStringValue("Organization", c.Config.Org).
		Msg("Service Detached From Application.")
	return nil
}

// DeleteService deletes a service specified by name
func (c *EpinioClient) DeleteService(name string, unbind bool) error {
	log := c.Log.WithName("Delete Service").
		WithValues("Name", name, "Organization", c.Config.Org)
	log.Info("start")
	defer log.Info("return")

	c.ui.Note().
		WithStringValue("Name", name).
		WithStringValue("Organization", c.Config.Org).
		Msg("Delete Service")

	request := models.DeleteRequest{
		Unbind: unbind,
	}

	js, err := json.Marshal(request)
	if err != nil {
		return err
	}

	jsonResponse, err := c.curlWithCustomErrorHandling(fmt.Sprintf("api/v1/orgs/%s/services/%s", c.Config.Org, name),
		"DELETE", string(js),
		func(response *http.Response, bodyBytes []byte, err error) error {
			// nothing special for internal errors and the like
			if response.StatusCode != http.StatusBadRequest {
				return err
			}

			// A bad request happens when the service is
			// still bound to one or omre applications,
			// and the response contains an array of their
			// names.

			var deleteResponse models.DeleteResponse
			if err := json.Unmarshal(bodyBytes, &deleteResponse); err != nil {
				return err
			}
			msg := c.ui.Exclamation().WithTable("Bound Applications")
			for _, app := range deleteResponse.BoundApps {
				msg = msg.WithTableRow(app)
			}

			msg.Msg("Unable to delete service. It is still used by")
			c.ui.Exclamation().Compact().Msg("Use --unbind to force the issue")

			return errors.New(http.StatusText(response.StatusCode))
		})
	if err != nil {
		if err.Error() != "Bad Request" {
			return err
		}
		return nil
	}

	if len(jsonResponse) > 0 {
		var deleteResponse models.DeleteResponse
		if err := json.Unmarshal(jsonResponse, &deleteResponse); err != nil {
			return err
		}
		if len(deleteResponse.BoundApps) > 0 {
			msg := c.ui.Note().WithTable("Previously Bound To")

			for _, app := range deleteResponse.BoundApps {
				msg = msg.WithTableRow(app)
			}

			msg.Msg("")
		}
	}

	c.ui.Success().
		WithStringValue("Name", name).
		WithStringValue("Organization", c.Config.Org).
		Msg("Service Removed.")
	return nil
}

// CreateService creates a service specified by name, class, plan, and optional key/value dictionary
// TODO: Allow underscores in service names (right now they fail because of kubernetes naming rules for secrets)
func (c *EpinioClient) CreateService(name, class, plan string, dict []string, waitForProvision bool) error {
	log := c.Log.WithName("Create Service").
		WithValues("Name", name, "Class", class, "Plan", plan, "Organization", c.Config.Org)
	log.Info("start")
	defer log.Info("return")

	data := make(map[string]string)
	msg := c.ui.Note().
		WithStringValue("Name", name).
		WithStringValue("Organization", c.Config.Org).
		WithStringValue("Class", class).
		WithStringValue("Plan", plan).
		WithTable("Parameter", "Value")
	for i := 0; i < len(dict); i += 2 {
		key := dict[i]
		value := dict[i+1]
		msg = msg.WithTableRow(key, value)
		data[key] = value
	}
	msg.Msg("Create Service")

	request := models.CatalogCreateRequest{
		Name:             name,
		Class:            class,
		Plan:             plan,
		Data:             data,
		WaitForProvision: waitForProvision,
	}

	js, err := json.Marshal(request)
	if err != nil {
		return err
	}

	if waitForProvision {
		c.ui.Note().KeeplineUnder(1).Msg("Provisioning...")
		s := c.ui.Progressf("Provisioning")
		defer s.Stop()
	}

	_, err = c.curl(fmt.Sprintf("api/v1/orgs/%s/services", c.Config.Org),
		"POST", string(js))
	if err != nil {
		return err
	}

	c.ui.Success().
		WithStringValue("Name", name).
		WithStringValue("Organization", c.Config.Org).
		WithStringValue("Class", class).
		WithStringValue("Plan", plan).
		Msg("Service Saved.")

	if waitForProvision {
		c.ui.Success().Msg("Service Provisioned.")
	} else {
		c.ui.Note().Msg(fmt.Sprintf("Use `epinio service %s` to watch when it is provisioned", name))
	}

	return nil
}

// CreateCustomService creates a service specified by name and key/value dictionary
// TODO: Allow underscores in service names (right now they fail because of kubernetes naming rules for secrets)
func (c *EpinioClient) CreateCustomService(name string, dict []string) error {
	log := c.Log.WithName("Create Custom Service").
		WithValues("Name", name, "Organization", c.Config.Org)
	log.Info("start")
	defer log.Info("return")

	data := make(map[string]string)
	msg := c.ui.Note().
		WithStringValue("Name", name).
		WithStringValue("Organization", c.Config.Org).
		WithTable("Parameter", "Value")
	for i := 0; i < len(dict); i += 2 {
		key := dict[i]
		value := dict[i+1]
		msg = msg.WithTableRow(key, value)
		data[key] = value
	}
	msg.Msg("Create Custom Service")

	request := models.CustomCreateRequest{
		Name: name,
		Data: data,
	}

	js, err := json.Marshal(request)
	if err != nil {
		return err
	}

	_, err = c.curl(fmt.Sprintf("api/v1/orgs/%s/custom-services", c.Config.Org),
		"POST", string(js))
	if err != nil {
		return err
	}

	c.ui.Success().
		WithStringValue("Name", name).
		WithStringValue("Organization", c.Config.Org).
		Msg("Service Saved.")
	return nil
}

// ServiceDetails shows the information of a service specified by name
func (c *EpinioClient) ServiceDetails(name string) error {
	log := c.Log.WithName("Service Details").
		WithValues("Name", name, "Organization", c.Config.Org)
	log.Info("start")
	defer log.Info("return")

	c.ui.Note().
		WithStringValue("Name", name).
		WithStringValue("Organization", c.Config.Org).
		Msg("Service Details")

	jsonResponse, err := c.curl(fmt.Sprintf("api/v1/orgs/%s/services/%s", c.Config.Org, name), "GET", "")
	if err != nil {
		return err
	}
	var serviceDetails map[string]string
	if err := json.Unmarshal(jsonResponse, &serviceDetails); err != nil {
		return err
	}

	msg := c.ui.Success().WithTable("", "")
	keys := make([]string, 0, len(serviceDetails))
	for k := range serviceDetails {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		msg = msg.WithTableRow(k, serviceDetails[k])
	}

	msg.Msg("")
	return nil
}

// Info displays information about environment
func (c *EpinioClient) Info() error {
	log := c.Log.WithName("Info")
	log.Info("start")
	defer log.Info("return")

	platform := c.KubeClient.GetPlatform()
	kubeVersion, err := c.KubeClient.GetVersion()
	if err != nil {
		return errors.Wrap(err, "failed to get kube version")
	}

	giteaVersion := "unavailable"

	version, resp, err := c.GiteaClient.Client.ServerVersion()
	if err == nil && resp != nil && resp.StatusCode == 200 {
		giteaVersion = version
	}

	c.ui.Success().
		WithStringValue("Platform", platform.String()).
		WithStringValue("Kubernetes Version", kubeVersion).
		WithStringValue("Gitea Version", giteaVersion).
		Msg("Epinio Environment")

	return nil
}

// AppsMatching returns all Epinio apps having the specified prefix
// in their name.
func (c *EpinioClient) AppsMatching(prefix string) []string {
	log := c.Log.WithName("AppsMatching").WithValues("PrefixToMatch", prefix)
	log.Info("start")
	defer log.Info("return")
	details := log.V(1) // NOTE: Increment of level, not absolute.

	result := []string{}

	apps, err := application.List(c.KubeClient, c.Config.Org)
	if err != nil {
		return result
	}

	for _, app := range apps {
		details.Info("Found", "Name", app.Name)

		if strings.HasPrefix(app.Name, prefix) {
			details.Info("Matched", "Name", app.Name)
			result = append(result, app.Name)
		}
	}

	return result
}

// Apps gets all Epinio apps in the targeted org
func (c *EpinioClient) Apps() error {
	log := c.Log.WithName("Apps").WithValues("Organization", c.Config.Org)
	log.Info("start")
	defer log.Info("return")
	details := log.V(1) // NOTE: Increment of level, not absolute.

	c.ui.Note().
		WithStringValue("Organization", c.Config.Org).
		Msg("Listing applications")

	details.Info("list applications")

	jsonResponse, err := c.curl(fmt.Sprintf("api/v1/orgs/%s/applications", c.Config.Org), "GET", "")
	if err != nil {
		return err
	}
	var apps application.ApplicationList
	if err := json.Unmarshal(jsonResponse, &apps); err != nil {
		return err
	}

	msg := c.ui.Success().WithTable("Name", "Status", "Routes", "Services")

	for _, app := range apps {
		msg = msg.WithTableRow(
			app.Name,
			app.Status,
			strings.Join(app.Routes, ", "),
			strings.Join(app.BoundServices, ", "))
	}

	msg.Msg("Epinio Applications:")

	return nil
}

// AppShow displays the information of the named app, in the targeted org
func (c *EpinioClient) AppShow(appName string) error {
	log := c.Log.WithName("Apps").WithValues("Organization", c.Config.Org, "Application", appName)
	log.Info("start")
	defer log.Info("return")
	details := log.V(1) // NOTE: Increment of level, not absolute.

	c.ui.Note().
		WithStringValue("Organization", c.Config.Org).
		WithStringValue("Application", appName).
		Msg("Show application details")

	details.Info("list applications")

	jsonResponse, err := c.curl(fmt.Sprintf("api/v1/orgs/%s/applications/%s", c.Config.Org, appName), "GET", "")
	if err != nil {
		return err
	}
	var app application.Application
	if err := json.Unmarshal(jsonResponse, &app); err != nil {
		return err
	}

	msg := c.ui.Success().WithTable("Key", "Value")

	msg = msg.WithTableRow("Status", app.Status)
	msg = msg.WithTableRow("Routes", strings.Join(app.Routes, ", "))
	msg = msg.WithTableRow("Services", strings.Join(app.BoundServices, ", "))

	msg.Msg("Details:")

	return nil
}

// CreateOrg creates an Org in gitea
func (c *EpinioClient) CreateOrg(org string) error {
	log := c.Log.WithName("CreateOrg").WithValues("Organization", org)
	log.Info("start")
	defer log.Info("return")

	c.ui.Note().
		WithStringValue("Name", org).
		Msg("Creating organization...")

	_, err := c.curl("api/v1/orgs", "POST", fmt.Sprintf(`{ "name": "%s" }`, org))
	if err != nil {
		return err
	}

	c.ui.Success().Msg("Organization created.")

	return nil
}

// Delete removes the named application from the cluster
func (c *EpinioClient) Delete(appname string) error {
	log := c.Log.WithName("Delete").WithValues("Application", appname)
	log.Info("start")
	defer log.Info("return")

	c.ui.Note().
		WithStringValue("Name", appname).
		WithStringValue("Organization", c.Config.Org).
		Msg("Deleting application...")

	s := c.ui.Progressf("Deleting %s in %s", appname, c.Config.Org)
	defer s.Stop()

	jsonResponse, err := c.curl(fmt.Sprintf("api/v1/orgs/%s/applications/%s", c.Config.Org, appname), "DELETE", "")
	if err != nil {
		return err
	}
	var response map[string][]string
	if err := json.Unmarshal(jsonResponse, &response); err != nil {
		return err
	}

	if !strings.Contains(c.GiteaClient.Domain, "omg.howdoi.website") {
		err = c.deleteProductionCertificate(appname)
		if err != nil {
			return errors.Wrap(err, "failed to delete production certificate")
		}
	} else {
		err = c.deleteLocalCertificate(appname)
		if err != nil {
			return errors.Wrap(err, "failed to delete local certificate")
		}
	}

	unboundServices, ok := response["UnboundServices"]
	if !ok {
		return errors.Errorf("bad response, expected key missing: %v", response)
	}
	if len(unboundServices) > 0 {
		s.Stop()
		msg := c.ui.Note().WithTable("Unbound Services")
		for _, bonded := range unboundServices {
			msg = msg.WithTableRow(bonded)
		}
		msg.Msg("")
	}

	c.ui.Success().Msg("Application deleted.")

	return nil
}

// OrgsMatching returns all Epinio orgs having the specified prefix in their name
func (c *EpinioClient) OrgsMatching(prefix string) []string {
	log := c.Log.WithName("OrgsMatching").WithValues("PrefixToMatch", prefix)
	log.Info("start")
	defer log.Info("return")
	details := log.V(1) // NOTE: Increment of level, not absolute.

	result := []string{}

	jsonResponse, err := c.curl("api/v1/orgs/", "GET", "")
	if err != nil {
		return result
	}

	var orgs []string
	if err := json.Unmarshal(jsonResponse, &orgs); err != nil {
		return result
	}

	for _, org := range orgs {
		details.Info("Found", "Name", org)

		if strings.HasPrefix(org, prefix) {
			details.Info("Matched", "Name", org)
			result = append(result, org)
		}
	}

	return result
}

func (c *EpinioClient) Orgs() error {
	log := c.Log.WithName("Orgs")
	log.Info("start")
	defer log.Info("return")
	details := log.V(1) // NOTE: Increment of level, not absolute.

	c.ui.Note().Msg("Listing organizations")

	details.Info("list organizations")
	jsonResponse, err := c.curl("api/v1/orgs/", "GET", "")
	if err != nil {
		return err
	}

	var orgs []string
	if err := json.Unmarshal(jsonResponse, &orgs); err != nil {
		return err
	}

	msg := c.ui.Success().WithTable("Name")

	for _, org := range orgs {
		msg = msg.WithTableRow(org)
	}

	msg.Msg("Epinio Organizations:")

	return nil
}

// Push pushes an app
func (c *EpinioClient) Push(app string, path string) error {
	log := c.Log.
		WithName("Push").
		WithValues("Name", app,
			"Organization", c.Config.Org,
			"Sources", path)
	log.Info("start")
	defer log.Info("return")
	details := log.V(1) // NOTE: Increment of level, not absolute.

	c.ui.Note().
		WithStringValue("Name", app).
		WithStringValue("Sources", path).
		WithStringValue("Organization", c.Config.Org).
		Msg("About to push an application with given name and sources into the specified organization")

	c.ui.Exclamation().
		Timeout(duration.UserAbort()).
		Msg("Hit Enter to continue or Ctrl+C to abort (deployment will continue automatically in 5 seconds)")

	// todo: fix, remove, move to server
	details.Info("validate")
	err := c.ensureGoodOrg(c.Config.Org, "Unable to push.")
	if err != nil {
		return err
	}

	details.Info("create repo")
	err = c.createRepo(app)
	if err != nil {
		return errors.Wrap(err, "create repo failed")
	}

	details.Info("create repo webhook")
	err = c.createRepoWebhook(app)
	if err != nil {
		return errors.Wrap(err, "webhook configuration failed")
	}

	details.Info("get app default route")
	route := c.appDefaultRoute(app)

	details.Info("prepare code")
	tmpDir, err := c.prepareCode(app, route, c.Config.Org, path)
	if err != nil {
		return errors.Wrap(err, "failed to prepare code")
	}
	defer os.RemoveAll(tmpDir)

	// Create production certificate if it is provided by user
	// else create a local cluster self-signed tls secret.
	if !strings.Contains(c.GiteaClient.Domain, "omg.howdoi.website") {
		details.Info("create production ready ssl certificate")
		err = c.createProductionCertificate(app, c.GiteaClient.Domain)
		if err != nil {
			return errors.Wrap(err, "create production ssl certificate failed")
		}
	} else {
		details.Info("create local ssl certificate")
		err = c.createLocalCertificate(app, c.GiteaClient.Domain)
		if err != nil {
			return errors.Wrap(err, "create local ssl certificate failed")
		}
	}

	details.Info("git push")
	err = c.gitPush(app, tmpDir)
	if err != nil {
		return errors.Wrap(err, "failed to git push code")
	}

	details.Info("start tailing logs")
	stopFunc, err := c.logs(app, c.Config.Org)
	if err != nil {
		return errors.Wrap(err, "failed to tail logs")
	}
	defer stopFunc()

	details.Info("wait for app")
	err = c.waitForApp(c.Config.Org, app)
	if err != nil {
		return errors.Wrap(err, "waiting for app failed")
	}

	c.ui.Success().
		WithStringValue("Name", app).
		WithStringValue("Organization", c.Config.Org).
		WithStringValue("Route", fmt.Sprintf("https://%s", route)).
		Msg("App is online.")

	return nil
}

// Target targets an org in gitea
func (c *EpinioClient) Target(org string) error {
	log := c.Log.WithName("Target").WithValues("Organization", org)
	log.Info("start")
	defer log.Info("return")
	details := log.V(1) // NOTE: Increment of level, not absolute.

	if org == "" {
		details.Info("query config")
		c.ui.Success().
			WithStringValue("Currently targeted organization", c.Config.Org).
			Msg("")
		return nil
	}

	c.ui.Note().
		WithStringValue("Name", org).
		Msg("Targeting organization...")

	// todo: fix, remove, move to server
	details.Info("validate")
	err := c.ensureGoodOrg(org, "Unable to target.")
	if err != nil {
		return err
	}

	details.Info("set config")
	c.Config.Org = org
	err = c.Config.Save()
	if err != nil {
		return errors.Wrap(err, "failed to save configuration")
	}

	c.ui.Success().Msg("Organization targeted.")

	return nil
}

func (c *EpinioClient) check() {
	c.GiteaClient.Client.GetMyUserInfo()
}

func (c *EpinioClient) createProductionCertificate(appName, systemDomain string) error {
	data := fmt.Sprintf(`{
		"apiVersion": "cert-manager.io/v1alpha2",
		"kind": "Certificate",
		"metadata": {
			"name": "%s.%s.ssl-certificate",
			"namespace": "%s"
		},
		"spec": {
			"commonName" : "%s.%s",
			"secretName" : "%s-tls",
			"dnsNames": [
				"%s.%s"
			],
			"issuerRef" : {
				"name" : "letsencrypt-production",
				"kind" : "ClusterIssuer"
			}
		}
    }`, c.Config.Org, appName, c.Config.Org, appName, systemDomain, appName, appName, systemDomain)

	decoderUnstructured := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
	obj := &unstructured.Unstructured{}
	_, _, err := decoderUnstructured.Decode([]byte(data), nil, obj)
	if err != nil {
		return err
	}

	certificateInstanceGVR := schema.GroupVersionResource{
		Group:    "cert-manager.io",
		Version:  "v1alpha2",
		Resource: "certificates",
	}

	dynamicClient, err := dynamic.NewForConfig(c.KubeClient.RestConfig)
	if err != nil {
		return err
	}

	_, err = dynamicClient.Resource(certificateInstanceGVR).Namespace(c.Config.Org).
		Create(context.Background(),
			obj,
			metav1.CreateOptions{})
	if err != nil {
		return err
	}

	return nil
}

func (c *EpinioClient) createLocalCertificate(appName, systemDomain string) error {
	data := fmt.Sprintf(`{
		"apiVersion": "quarks.cloudfoundry.org/v1alpha1",
		"kind": "QuarksSecret",
		"metadata": {
			"name": "%s.%s.generate-certificate",
			"namespace": "%s"
		},
		"spec": {
			"request" : {
				"certificate" : {
					"CAKeyRef" : {
						"key" : "private_key",
						"name" : "ca-cert"
					},
					"CARef" : {
						"key" : "certificate",
						"name" : "ca-cert"
					},
					"commonName" : "%s.%s",
					"isCA" : false,
					"alternativeNames": [
						"%s.%s"
					],
					"signerType" : "local"
				}
			},
			"secretName" : "%s-tls",
			"type" : "tls"
		}
    }`, c.Config.Org, appName, c.Config.Org, appName, systemDomain, appName, systemDomain, appName)

	decoderUnstructured := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
	obj := &unstructured.Unstructured{}
	_, _, err := decoderUnstructured.Decode([]byte(data), nil, obj)
	if err != nil {
		return err
	}

	quarksSecretInstanceGVR := schema.GroupVersionResource{
		Group:    "quarks.cloudfoundry.org",
		Version:  "v1alpha1",
		Resource: "quarkssecrets",
	}

	dynamicClient, err := dynamic.NewForConfig(c.KubeClient.RestConfig)
	if err != nil {
		return err
	}

	_, _, err = decoderUnstructured.Decode([]byte(data), nil, obj)
	if err != nil {
		return err
	}

	_, err = dynamicClient.Resource(quarksSecretInstanceGVR).Namespace(c.Config.Org).
		Create(context.Background(),
			obj,
			metav1.CreateOptions{})
	if err != nil {
		return err
	}

	return nil
}

func (c *EpinioClient) deleteLocalCertificate(appName string) error {
	quarksSecretInstanceGVR := schema.GroupVersionResource{
		Group:    "quarks.cloudfoundry.org",
		Version:  "v1alpha1",
		Resource: "quarkssecrets",
	}

	dynamicClient, err := dynamic.NewForConfig(c.KubeClient.RestConfig)
	if err != nil {
		return err
	}

	err = dynamicClient.Resource(quarksSecretInstanceGVR).Namespace(c.Config.Org).
		Delete(context.Background(),
			fmt.Sprintf("%s.%s.generate-certificate", c.Config.Org, appName),
			metav1.DeleteOptions{})
	if err != nil {
		return err
	}

	return nil
}

func (c *EpinioClient) deleteProductionCertificate(appName string) error {
	certificateInstanceGVR := schema.GroupVersionResource{
		Group:    "cert-manager.io",
		Version:  "v1alpha2",
		Resource: "certificates",
	}

	dynamicClient, err := dynamic.NewForConfig(c.KubeClient.RestConfig)
	if err != nil {
		return err
	}

	err = dynamicClient.Resource(certificateInstanceGVR).Namespace(c.Config.Org).
		Delete(context.Background(),
			fmt.Sprintf("%s.%s.ssl-certificate", c.Config.Org, appName),
			metav1.DeleteOptions{})
	if err != nil {
		return err
	}

	err = c.KubeClient.Kubectl.CoreV1().Secrets(c.Config.Org).Delete(context.Background(), fmt.Sprintf("%s-tls", appName), metav1.DeleteOptions{})
	if err != nil {
		return err
	}

	return nil
}

func (c *EpinioClient) createRepo(name string) error {
	_, resp, err := c.GiteaClient.Client.GetRepo(c.Config.Org, name)
	if resp == nil && err != nil {
		return errors.Wrap(err, "failed to make get repo request")
	}

	if resp.StatusCode == 200 {
		c.ui.Note().Msg("Application already exists. Updating.")
		return nil
	}

	_, _, err = c.GiteaClient.Client.CreateOrgRepo(c.Config.Org, gitea.CreateRepoOption{
		Name:          name,
		AutoInit:      true,
		Private:       true,
		DefaultBranch: "main",
	})

	if err != nil {
		return errors.Wrap(err, "failed to create application")
	}

	c.ui.Success().Msg("Application Repository created.")

	return nil
}

func (c *EpinioClient) createRepoWebhook(name string) error {
	hooks, _, err := c.GiteaClient.Client.ListRepoHooks(c.Config.Org, name, gitea.ListHooksOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to list webhooks")
	}

	for _, hook := range hooks {
		url := hook.Config["url"]
		if url == StagingEventListenerURL {
			c.ui.Normal().Msg("Webhook already exists.")
			return nil
		}
	}

	c.ui.Normal().Msg("Creating webhook in the repo...")

	c.GiteaClient.Client.CreateRepoHook(c.Config.Org, name, gitea.CreateHookOption{
		Active:       true,
		BranchFilter: "*",
		Config: map[string]string{
			"secret":       HookSecret,
			"http_method":  "POST",
			"url":          StagingEventListenerURL,
			"content_type": "json",
		},
		Type: "gitea",
	})

	return nil
}

func (c *EpinioClient) appDefaultRoute(name string) string {
	return fmt.Sprintf("%s.%s", name, c.GiteaClient.Domain)
}

func (c *EpinioClient) prepareCode(name, route, org, appDir string) (tmpDir string, err error) {
	c.ui.Normal().Msg("Preparing code ...")

	tmpDir, err = ioutil.TempDir("", "epinio-app")
	if err != nil {
		return "", errors.Wrap(err, "can't create temp directory")
	}

	err = copy.Copy(appDir, tmpDir)
	if err != nil {
		return "", errors.Wrap(err, "failed to copy app sources to temp location")
	}

	err = os.MkdirAll(filepath.Join(tmpDir, ".kube"), 0700)
	if err != nil {
		return "", errors.Wrap(err, "failed to setup kube resources directory in temp app location")
	}

	if err := c.renderDeployment(filepath.Join(tmpDir, ".kube", "app.yml"), name); err != nil {
		return "", err
	}
	if err := c.renderService(filepath.Join(tmpDir, ".kube", "service.yml"), name); err != nil {
		return "", err
	}
	if err := c.renderIngress(filepath.Join(tmpDir, ".kube", "ingress.yml"), name, route); err != nil {
		return "", err
	}

	return
}

func (c *EpinioClient) renderDeployment(filePath, appName string) error {
	route := c.appDefaultRoute(appName)

	deploymentTmpl, err := template.New("deployment").Parse(`
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: "{{ .AppName }}"
  namespace: {{ .Org }}
  labels:
    app.kubernetes.io/name: "{{ .AppName }}"
    app.kubernetes.io/part-of: "{{ .Org }}"
    app.kubernetes.io/component: application
    app.kubernetes.io/managed-by: epinio
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: "{{ .AppName }}"
  template:
    metadata:
      labels:
        app.kubernetes.io/name: "{{ .AppName }}"
        app.kubernetes.io/part-of: "{{ .Org }}"
        app.kubernetes.io/component: application
        app.kubernetes.io/managed-by: epinio
      annotations:
        app.kubernetes.io/name: "{{ .AppName }}"
    spec:
      # TODO: Do these when you create an org
      serviceAccountName: {{ .Org }}
      automountServiceAccountToken: false
      containers:
      - name: "{{ .AppName }}"
        image: "127.0.0.1:30500/apps/{{ .AppName }}"
        ports:
        - containerPort: 8080
        env:
        - name: PORT
          value: "8080"
  `)
	if err != nil {
		return errors.Wrap(err, "failed to parse deployment template for app")
	}

	appFile, err := os.Create(filePath)
	if err != nil {
		return errors.Wrap(err, "failed to create file for kube resource definitions")
	}
	defer func() { err = appFile.Close() }()

	err = deploymentTmpl.Execute(appFile, struct {
		AppName string
		Route   string
		Org     string
	}{
		AppName: appName,
		Route:   route,
		Org:     c.Config.Org,
	})

	if err != nil {
		return errors.Wrap(err, "failed to render kube resource definition")
	}

	return nil
}

func (c *EpinioClient) renderService(filePath, appName string) error {
	serviceTmpl, err := template.New("service").Parse(`
apiVersion: v1
kind: Service
metadata:
  annotations:
    traefik.ingress.kubernetes.io/router.entrypoints: websecure
    traefik.ingress.kubernetes.io/router.tls: "true"
  labels:
    app.kubernetes.io/component: application
    app.kubernetes.io/managed-by: epinio
    app.kubernetes.io/name: {{ .AppName }}
    app.kubernetes.io/part-of: {{ .Org }}
    kubernetes.io/ingress.class: traefik
  name: {{ .AppName }}
  namespace: {{ .Org }}
spec:
  ports:
  - port: 8080
    protocol: TCP
    targetPort: 8080
  selector:
    app.kubernetes.io/name: "{{ .AppName }}"
  type: ClusterIP
  `)
	if err != nil {
		return errors.Wrap(err, "failed to parse service template for app")
	}

	serviceFile, err := os.Create(filePath)
	if err != nil {
		return errors.Wrap(err, "failed to create file for application Service definition")
	}
	defer func() { _ = serviceFile.Close() }()

	err = serviceTmpl.Execute(serviceFile, struct {
		AppName string
		Org     string
	}{
		AppName: appName,
		Org:     c.Config.Org,
	})
	if err != nil {
		return errors.Wrap(err, "failed to render application Service definition")
	}

	return nil
}

func (c *EpinioClient) renderIngress(filePath, appName, route string) error {
	ingressTmpl, err := template.New("ingress").Parse(`
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  annotations:
    traefik.ingress.kubernetes.io/router.entrypoints: websecure
    traefik.ingress.kubernetes.io/router.tls: "true"
  labels:
    app.kubernetes.io/component: application
    app.kubernetes.io/managed-by: epinio
    app.kubernetes.io/name: {{ .AppName }}
    app.kubernetes.io/part-of: {{ .Org }}
    kubernetes.io/ingress.class: traefik
  name: {{ .AppName }}
  namespace: {{ .Org }}
spec:
  rules:
  - host: {{ .Route }}
    http:
      paths:
      - backend:
          service:
            name: {{ .AppName }}
            port:
              number: 8080
        path: /
        pathType: ImplementationSpecific
  tls:
  - hosts:
    - {{ .Route }}
    secretName: {{ .AppName }}-tls
  `)
	if err != nil {
		return errors.Wrap(err, "failed to parse ingress template for app")
	}

	ingressFile, err := os.Create(filePath)
	if err != nil {
		return errors.Wrap(err, "failed to create file for application Ingress definition")
	}
	defer func() { err = ingressFile.Close() }()

	err = ingressTmpl.Execute(ingressFile, struct {
		AppName string
		Org     string
		Route   string
	}{
		AppName: appName,
		Org:     c.Config.Org,
		Route:   route,
	})
	if err != nil {
		return errors.Wrap(err, "failed to render application Ingress definition")
	}

	return nil
}

func (c *EpinioClient) gitPush(name, tmpDir string) error {
	c.ui.Normal().Msg("Pushing application code ...")

	u, err := url.Parse(c.GiteaClient.URL)
	if err != nil {
		return errors.Wrap(err, "failed to parse gitea url")
	}

	u.User = url.UserPassword(c.GiteaClient.Username, c.GiteaClient.Password)
	u.Path = path.Join(u.Path, c.Config.Org, name)

	cmd := exec.Command("/bin/sh", "-c", fmt.Sprintf(`
cd "%s" 
git init
git config user.name "Epinio"
git config user.email ci@epinio
git remote add epinio "%s"
git fetch --all
git reset --soft epinio/main
git add --all
git commit -m "pushed at %s"
git push epinio %s:main
`, tmpDir, u.String(), time.Now().Format("20060102150405"), "`git branch --show-current`"))

	output, err := cmd.CombinedOutput()
	if err != nil {
		c.ui.Problem().
			WithStringValue("Stdout", string(output)).
			WithStringValue("Stderr", "").
			Msg("App push failed")
		return errors.Wrap(err, "push script failed")
	}

	c.ui.Note().V(1).WithStringValue("Output", string(output)).Msg("")
	c.ui.Success().Msg("Application push successful")

	return nil
}

func (c *EpinioClient) logs(appName, org string) (context.CancelFunc, error) {
	c.ui.ProgressNote().V(1).Msg("Tailing application logs ...")

	ctx, cancelFunc := context.WithCancel(context.Background())

	// TODO: improve the way we look for pods, use selectors
	// and watch staging as well
	err := tailer.Run(c.ui, ctx, &tailer.Config{
		ContainerQuery:        regexp.MustCompile(".*"),
		ExcludeContainerQuery: nil,
		ContainerState:        "running",
		Exclude:               nil,
		Include:               nil,
		Timestamps:            false,
		Since:                 duration.LogHistory(),
		AllNamespaces:         false,
		LabelSelector:         labels.Everything(),
		TailLines:             nil,
		Template:              tailer.DefaultSingleNamespaceTemplate(),
		Namespace:             org,
		PodQuery:              regexp.MustCompile(fmt.Sprintf(".*-%s-.*", appName)),
	}, c.KubeClient)
	if err != nil {
		return cancelFunc, errors.Wrap(err, "failed to start log tail")
	}

	return cancelFunc, nil
}

func (c *EpinioClient) waitForApp(org, name string) error {
	c.ui.ProgressNote().KeeplineUnder(1).Msg("Creating application resources")
	err := c.KubeClient.WaitUntilPodBySelectorExist(
		c.ui, org, fmt.Sprintf("app.kubernetes.io/name=%s", name),
		duration.ToAppBuilt())
	if err != nil {
		return errors.Wrap(err, "waiting for app to be created failed")
	}

	c.ui.ProgressNote().KeeplineUnder(1).Msg("Starting application")

	err = c.KubeClient.WaitForPodBySelectorRunning(
		c.ui, org, fmt.Sprintf("app.kubernetes.io/name=%s", name),
		duration.ToPodReady())

	if err != nil {
		return errors.Wrap(err, "waiting for app to come online failed")
	}

	return nil
}

// TODO: Delete after all commands go through the api
func (c *EpinioClient) ensureGoodOrg(org, msg string) error {
	_, resp, err := c.GiteaClient.Client.GetOrg(org)
	if resp == nil && err != nil {
		return errors.Wrap(err, "failed to make get org request")
	}

	if resp.StatusCode == 404 {
		errmsg := "Organization does not exist."
		if msg != "" {
			errmsg += " " + msg
		}
		c.ui.Exclamation().WithEnd(1).Msg(errmsg)
	}

	return nil
}

func (c *EpinioClient) ServicesToApps(org string) (map[string]application.ApplicationList, error) {
	// Determine apps bound to services
	// (inversion of services bound to apps)
	// Literally query apps in the org for their services and invert.

	var appsOf = map[string]application.ApplicationList{}

	apps, err := application.List(c.KubeClient, c.Config.Org)
	if err != nil {
		return nil, err
	}

	for _, app := range apps {
		bound, err := app.Services()
		if err != nil {
			return nil, err
		}
		for _, bonded := range bound {
			bname := bonded.Name()
			if theapps, found := appsOf[bname]; found {
				appsOf[bname] = append(theapps, app)
			} else {
				appsOf[bname] = application.ApplicationList{app}
			}
		}
	}

	return appsOf, nil
}

func (c *EpinioClient) curl(endpoint, method, requestBody string) ([]byte, error) {
	uri := fmt.Sprintf("%s/%s", c.serverURL, endpoint)
	request, err := http.NewRequest(method, uri, strings.NewReader(requestBody))
	if err != nil {
		return []byte{}, err
	}
	response, err := (&http.Client{}).Do(request)
	if err != nil {
		return []byte{}, err
	}
	defer response.Body.Close()

	bodyBytes, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return []byte{}, err
	}

	if response.StatusCode == http.StatusCreated {
		return bodyBytes, nil
	}

	if response.StatusCode != http.StatusOK {
		return []byte{}, errors.New(fmt.Sprintf("%s: %s", http.StatusText(response.StatusCode), string(bodyBytes)))
	}

	return bodyBytes, nil
}

func (c *EpinioClient) curlWithCustomErrorHandling(endpoint, method, requestBody string,
	f func(response *http.Response, bodyBytes []byte, err error) error) ([]byte, error) {

	uri := fmt.Sprintf("%s/%s", c.serverURL, endpoint)
	request, err := http.NewRequest(method, uri, strings.NewReader(requestBody))
	if err != nil {
		return []byte{}, err
	}
	response, err := (&http.Client{}).Do(request)
	if err != nil {
		return []byte{}, err
	}
	defer response.Body.Close()

	bodyBytes, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return []byte{}, err
	}

	if response.StatusCode == http.StatusCreated {
		return bodyBytes, nil
	}

	if response.StatusCode != http.StatusOK {
		return []byte{}, f(response, bodyBytes,
			errors.New(fmt.Sprintf("%s: %s", http.StatusText(response.StatusCode), string(bodyBytes))))
	}

	return bodyBytes, nil
}
