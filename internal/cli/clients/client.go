// Package clients contains all the CLI commands for the client
package clients

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/avast/retry-go"
	"github.com/epinio/epinio/deployments"
	"github.com/epinio/epinio/helpers/kubernetes"
	"github.com/epinio/epinio/helpers/kubernetes/tailer"
	"github.com/epinio/epinio/helpers/termui"
	"github.com/epinio/epinio/helpers/tracelog"
	api "github.com/epinio/epinio/internal/api/v1"
	"github.com/epinio/epinio/internal/api/v1/models"
	"github.com/epinio/epinio/internal/application"
	"github.com/epinio/epinio/internal/cli/config"
	"github.com/epinio/epinio/internal/cli/logprinter"
	"github.com/epinio/epinio/internal/domain"
	"github.com/epinio/epinio/internal/duration"
	"github.com/epinio/epinio/internal/services"

	"github.com/go-logr/logr"
	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/util/validation"
)

// EpinioClient provides functionality for talking to a
// Epinio installation on Kubernetes
type EpinioClient struct {
	Cluster     *kubernetes.Cluster
	Config      *config.Config
	Log         logr.Logger
	ui          *termui.UI
	serverURL   string
	wsServerURL string
}

type PushParams struct {
	Instances *int32
	Services  []string
}

func NewEpinioClient(ctx context.Context) (*EpinioClient, error) {
	configConfig, err := config.Load()
	if err != nil {
		return nil, err
	}

	cluster, err := kubernetes.GetCluster(ctx)
	if err != nil {
		return nil, err
	}

	uiUI := termui.NewUI()
	epClient, err := GetEpinioAPIClient(ctx)
	if err != nil {
		return nil, err
	}
	serverURL := epClient.URL
	wsServerURL := epClient.WsURL

	logger := tracelog.NewClientLogger()
	epinioClient := &EpinioClient{
		Cluster:     cluster,
		ui:          uiUI,
		Config:      configConfig,
		Log:         logger,
		serverURL:   serverURL,
		wsServerURL: wsServerURL,
	}
	return epinioClient, nil
}

// EnvList displays a table of all environment variables and their
// values for the named application.
func (c *EpinioClient) EnvList(ctx context.Context, appName string) error {
	log := c.Log.WithName("EnvList")
	log.Info("start")
	defer log.Info("return")

	c.ui.Note().
		WithStringValue("Organization", c.Config.Org).
		WithStringValue("Application", appName).
		Msg("Show Application Environment")

	jsonResponse, err := c.get(api.Routes.Path("EnvList", c.Config.Org, appName))
	if err != nil {
		return err
	}

	var eVariables models.EnvVariableList
	if err := json.Unmarshal(jsonResponse, &eVariables); err != nil {
		return err
	}

	msg := c.ui.Success().WithTable("Variable", "Value")

	sort.Sort(eVariables)
	for _, ev := range eVariables {
		msg = msg.WithTableRow(ev.Name, ev.Value)
	}

	msg.Msg("Ok")
	return nil
}

// EnvUnset adds or modifies the specified environment variable in the
// named application, with the given value. A workload is restarted.
func (c *EpinioClient) EnvSet(ctx context.Context, appName, envName, envValue string) error {
	log := c.Log.WithName("Env")
	log.Info("start")
	defer log.Info("return")

	c.ui.Note().
		WithStringValue("Organization", c.Config.Org).
		WithStringValue("Application", appName).
		WithStringValue("Variable", envName).
		WithStringValue("Value", envValue).
		Msg("Extend or modify application environment")

	request := models.EnvVariableList{
		models.EnvVariable{
			Name:  envName,
			Value: envValue,
		},
	}

	js, err := json.Marshal(request)
	if err != nil {
		return err
	}

	_, err = c.post(api.Routes.Path("EnvSet", c.Config.Org, appName), string(js))
	if err != nil {
		return err
	}

	c.ui.Success().Msg("OK")
	return nil
}

// EnvUnset shows the value of the specified environment variable in
// the named application.
func (c *EpinioClient) EnvShow(ctx context.Context, appName, envName string) error {
	log := c.Log.WithName("Env")
	log.Info("start")
	defer log.Info("return")

	c.ui.Note().
		WithStringValue("Organization", c.Config.Org).
		WithStringValue("Application", appName).
		WithStringValue("Variable", envName).
		Msg("Show application environment variable")

	jsonResponse, err := c.get(api.Routes.Path("EnvShow", c.Config.Org, appName, envName))
	if err != nil {
		return err
	}

	var eVariable models.EnvVariable
	if err := json.Unmarshal(jsonResponse, &eVariable); err != nil {
		return err
	}

	c.ui.Success().
		WithStringValue("Value", eVariable.Value).
		Msg("OK")

	return nil
}

// EnvUnset removes the specified environment variable from the named
// application. A workload is restarted.
func (c *EpinioClient) EnvUnset(ctx context.Context, appName, envName string) error {
	log := c.Log.WithName("Env")
	log.Info("start")
	defer log.Info("return")

	c.ui.Note().
		WithStringValue("Organization", c.Config.Org).
		WithStringValue("Application", appName).
		WithStringValue("Variable", envName).
		Msg("Remove from application environment")

	_, err := c.delete(api.Routes.Path("EnvUnset", c.Config.Org, appName, envName))
	if err != nil {
		return err
	}

	c.ui.Success().Msg("OK")

	return nil
}

// EnvMatching retrieves all environment variables in the cluster, for
// the specified application, and the given prefix
func (c *EpinioClient) EnvMatching(ctx context.Context, appName, prefix string) []string {
	log := c.Log.WithName("Env")
	log.Info("start")
	defer log.Info("return")

	jsonResponse, err := c.get(api.Routes.Path("EnvMatch", c.Config.Org, appName, prefix))
	if err != nil {
		return []string{}
	}

	var evNames []string
	if err := json.Unmarshal(jsonResponse, &evNames); err != nil {
		return []string{}
	}

	return evNames
}

// ConfigUpdate updates the credentials stored in the config from the
// currently targeted kube cluster
func (c *EpinioClient) ConfigUpdate(ctx context.Context) error {
	log := c.Log.WithName("ConfigUpdate")
	log.Info("start")
	defer log.Info("return")
	details := log.V(1) // NOTE: Increment of level, not absolute.

	c.ui.Note().
		Msg("Updating the stored credentials from the current cluster")

	user, password, err := getCredentials(details, ctx)
	if err != nil {
		c.ui.Exclamation().Msg(err.Error())
		return nil
	}

	certs, err := getCerts(ctx, details)
	if err != nil {
		c.ui.Exclamation().Msg(err.Error())
		return nil
	}

	c.Config.User = user
	c.Config.Password = password
	c.Config.Certs = certs

	details.Info("Saving", "User", c.Config.User, "Pass", c.Config.Password, "Cert", c.Config.Certs)

	err = c.Config.Save()
	if err != nil {
		c.ui.Exclamation().Msg(errors.Wrap(err, "failed to save configuration").Error())
		return nil
	}

	c.ui.Success().Msg("Ok")
	return nil
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

	jsonResponse, err := c.get(api.Routes.Path("ServicePlans", serviceClassName))
	if err != nil {
		return err
	}
	var servicePlans services.ServicePlanList
	if err := json.Unmarshal(jsonResponse, &servicePlans); err != nil {
		return err
	}

	details.Info("list service plans")

	sort.Sort(servicePlans)
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
func (c *EpinioClient) ServicePlanMatching(ctx context.Context, serviceClassName, prefix string) []string {
	log := c.Log.WithName("ServicePlans").WithValues("PrefixToMatch", prefix)
	log.Info("start")
	defer log.Info("return")
	details := log.V(1) // NOTE: Increment of level, not absolute.

	result := []string{}

	// TODO Create and use server endpoints. Maybe use existing
	// `Index`/Listing endpoint, either with parameter for
	// matching, or local matching.

	serviceClass, err := services.ClassLookup(ctx, c.Cluster, serviceClassName)
	if err != nil {
		return result
	}

	servicePlans, err := serviceClass.ListPlans(ctx)
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
func (c *EpinioClient) ServiceClassMatching(ctx context.Context, prefix string) []string {
	log := c.Log.WithName("ServiceClasses").WithValues("PrefixToMatch", prefix)
	log.Info("start")
	defer log.Info("return")
	details := log.V(1) // NOTE: Increment of level, not absolute.

	result := []string{}

	// TODO Create and use server endpoints. Maybe use existing
	// `Index`/Listing endpoint, either with parameter for
	// matching, or local matching.

	serviceClasses, err := services.ListClasses(ctx, c.Cluster)
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

	jsonResponse, err := c.get(api.Routes.Path("ServiceClasses"))
	if err != nil {
		return err
	}
	var serviceClasses services.ServiceClassList
	if err := json.Unmarshal(jsonResponse, &serviceClasses); err != nil {
		return err
	}

	details.Info("list service classes")

	sort.Sort(serviceClasses)
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

	jsonResponse, err := c.get(api.Routes.Path("Services", c.Config.Org))
	if err != nil {
		return err
	}
	var response models.ServiceResponseList
	if err := json.Unmarshal(jsonResponse, &response); err != nil {
		return err
	}

	details.Info("list services")

	sort.Sort(response)
	msg := c.ui.Success().WithTable("Name", "Applications")

	details.Info("list services")
	for _, service := range response {
		msg = msg.WithTableRow(service.Name, strings.Join(service.BoundApps, ", "))
	}
	msg.Msg("Epinio Services:")

	return nil
}

// ServiceMatching returns all Epinio services having the specified prefix
// in their name.
func (c *EpinioClient) ServiceMatching(ctx context.Context, prefix string) []string {
	log := c.Log.WithName("ServiceMatching").WithValues("PrefixToMatch", prefix)
	log.Info("start")
	defer log.Info("return")
	details := log.V(1) // NOTE: Increment of level, not absolute.

	result := []string{}

	// TODO Create and use server endpoints. Maybe use existing
	// `Index`/Listing endpoint, either with parameter for
	// matching, or local matching.

	orgServices, err := services.List(ctx, c.Cluster, c.Config.Org)
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
		Names: []string{serviceName},
	}

	js, err := json.Marshal(request)
	if err != nil {
		return err
	}

	b, err := c.post(api.Routes.Path("ServiceBindingCreate", c.Config.Org, appName), string(js))
	if err != nil {
		return err
	}

	br := &models.BindResponse{}
	if err := json.Unmarshal(b, br); err != nil {
		return err
	}

	if len(br.WasBound) > 0 {
		c.ui.Success().
			WithStringValue("Service", serviceName).
			WithStringValue("Application", appName).
			WithStringValue("Organization", c.Config.Org).
			Msg("Service Already Bound to Application.")

		return nil
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

	_, err := c.delete(api.Routes.Path("ServiceBindingDelete",
		c.Config.Org, appName, serviceName))
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

	jsonResponse, err := c.curlWithCustomErrorHandling(
		api.Routes.Path("ServiceDelete", c.Config.Org, name),
		"DELETE", string(js),
		func(response *http.Response, bodyBytes []byte, err error) error {
			// nothing special for internal errors and the like
			if response.StatusCode != http.StatusBadRequest {
				return err
			}

			// A bad request happens when the service is
			// still bound to one or more applications,
			// and the response contains an array of their
			// names.

			var apiError api.ErrorResponse
			if err := json.Unmarshal(bodyBytes, &apiError); err != nil {
				return err
			}

			bound := strings.Split(apiError.Errors[0].Details, ",")

			sort.Strings(bound)
			msg := c.ui.Exclamation().WithTable("Bound Applications")

			for _, app := range bound {
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
			sort.Strings(deleteResponse.BoundApps)
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
func (c *EpinioClient) CreateService(name, class, plan string, data string, waitForProvision bool) error {
	log := c.Log.WithName("Create Service").
		WithValues("Name", name, "Class", class, "Plan", plan, "Organization", c.Config.Org)
	log.Info("start")
	defer log.Info("return")

	msg := c.ui.Note().
		WithStringValue("Name", name).
		WithStringValue("Organization", c.Config.Org).
		WithStringValue("Class", class).
		WithStringValue("Plan", plan).
		WithTable("Parameter", "Value")
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

	_, err = c.post(api.Routes.Path("ServiceCreate", c.Config.Org), string(js))
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

	_, err = c.post(api.Routes.Path("ServiceCreateCustom", c.Config.Org),
		string(js))
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

	jsonResponse, err := c.get(api.Routes.Path("ServiceShow", c.Config.Org, name))
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

	platform := c.Cluster.GetPlatform()
	kubeVersion, err := c.Cluster.GetVersion()
	if err != nil {
		return errors.Wrap(err, "failed to get kube version")
	}

	// TODO: Extend the epinio API to get the gitea version
	// information again. Or remove it entirely.

	giteaVersion := "unavailable"

	epinioVersion := "unavailable"
	if jsonResponse, err := c.get(api.Routes.Path("Info")); err == nil {
		v := struct{ Version string }{}
		if err := json.Unmarshal(jsonResponse, &v); err == nil {
			epinioVersion = v.Version
		}
	}

	c.ui.Success().
		WithStringValue("Platform", platform.String()).
		WithStringValue("Kubernetes Version", kubeVersion).
		WithStringValue("Gitea Version", giteaVersion).
		WithStringValue("Epinio Version", epinioVersion).
		Msg("Epinio Environment")

	return nil
}

// AppsMatching returns all Epinio apps having the specified prefix
// in their name.
func (c *EpinioClient) AppsMatching(ctx context.Context, prefix string) []string {
	log := c.Log.WithName("AppsMatching").WithValues("PrefixToMatch", prefix)
	log.Info("start")
	defer log.Info("return")
	details := log.V(1) // NOTE: Increment of level, not absolute.

	result := []string{}

	// TODO Create and use server endpoints. Maybe use existing
	// `Index`/Listing endpoint, either with parameter for
	// matching, or local matching.

	apps, err := application.List(ctx, c.Cluster, c.Config.Org)
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

	jsonResponse, err := c.get(api.Routes.Path("Apps", c.Config.Org))
	if err != nil {
		return err
	}

	var apps models.AppList
	if err := json.Unmarshal(jsonResponse, &apps); err != nil {
		return err
	}

	sort.Sort(apps)
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

	details.Info("show application")

	jsonResponse, err := c.get(api.Routes.Path("AppShow", c.Config.Org, appName))
	if err != nil {
		return err
	}
	var app models.App
	if err := json.Unmarshal(jsonResponse, &app); err != nil {
		return err
	}

	c.ui.Success().
		WithTable("Key", "Value").
		WithTableRow("Status", app.Status).
		WithTableRow("StageId", app.StageID).
		WithTableRow("Routes", strings.Join(app.Routes, ", ")).
		WithTableRow("Services", strings.Join(app.BoundServices, ", ")).
		WithTableRow("Environment", `See it by running the command "epinio app env list `+appName+`"`).
		Msg("Details:")

	return nil
}

// AppStageID returns the stage id of the named app, in the targeted org
func (c *EpinioClient) AppStageID(appName string) (string, error) {
	log := c.Log.WithName("Apps").WithValues("Organization", c.Config.Org, "Application", appName)
	log.Info("start")
	defer log.Info("return")

	jsonResponse, err := c.get(api.Routes.Path("AppShow", c.Config.Org, appName))
	if err != nil {
		return "", err
	}
	var app models.App
	if err := json.Unmarshal(jsonResponse, &app); err != nil {
		return "", err
	}

	if !app.Active {
		return "", errors.New("Application has no workload")
	}

	return app.StageID, nil
}

// AppUpdate updates the specified running application's attributes (e.g. instances)
func (c *EpinioClient) AppUpdate(appName string, instances int32) error {
	log := c.Log.WithName("Apps").WithValues("Organization", c.Config.Org, "Application", appName)
	log.Info("start")
	defer log.Info("return")
	details := log.V(1) // NOTE: Increment of level, not absolute.

	c.ui.Note().
		WithStringValue("Organization", c.Config.Org).
		WithStringValue("Application", appName).
		Msg("Update application")

	details.Info("update application")

	data, err := json.Marshal(models.UpdateAppRequest{
		Instances: instances,
	})
	if err != nil {
		return err
	}
	_, err = c.patch(
		api.Routes.Path("AppUpdate", c.Config.Org, appName), string(data))
	if err != nil {
		return err
	}

	c.ui.Success().Msg("Successfully updated application")

	return nil
}

// AppLogs streams the logs of all the application instances, in the targeted org
// If stageID is an empty string, runtime application logs are streamed. If stageID
// is set, then the matching staging logs are streamed.
// There are 2 ways of stopping this method:
// 1. The websocket connection closes.
// 2. Something is sent to the interrupt channel
// The interrupt channel is used by the caller when printing of logs should
// be stopped.
// To make sure everything is properly stopped (both the main thread and the
// go routine) no matter what caused the stop (number 1 or 2 above):
// - The go routines closes the connection on interrupt. This causes the main
//   loop to stop as well.
// - The main thread sends a signal to the `done` channel when it returns. This
//   causes the go routine to stop.
// - The main thread waits for the go routine to stop before finally returning (by
//   calling `wg.Wait()`.
// This is what happens when `interrupt` receives something:
// 1. The go routine closes the connection
// 2. The loop in the main thread is stopped because the connection was closed
// 3. The main thread sends to the `done` chan (as a "defer" function), and then
//    calls wg.Wait() to wait for the go routine to exit.
// 4. The go routine receives the `done` message, calls wg.Done() and returns
// 5. The main thread returns
// When the connection is closed (e.g. from the server side), the process is the
// same but starts from #2 above.
func (c *EpinioClient) AppLogs(appName, stageID string, follow bool, interrupt chan bool) error {
	log := c.Log.WithName("Apps").WithValues("Organization", c.Config.Org, "Application", appName)
	log.Info("start")
	defer log.Info("return")
	details := log.V(1) // NOTE: Increment of level, not absolute.

	c.ui.Note().
		WithStringValue("Organization", c.Config.Org).
		WithStringValue("Application", appName).
		Msg("Streaming application logs")

	details.Info("application logs")

	var urlArgs = []string{}
	urlArgs = append(urlArgs, fmt.Sprintf("follow=%t", follow))
	urlArgs = append(urlArgs, fmt.Sprintf("stage_id=%s", stageID))

	headers := http.Header{
		"Authorization": {"Basic " + base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", c.Config.User, c.Config.Password)))},
	}

	var endpoint string
	if stageID == "" {
		endpoint = api.Routes.Path("AppLogs", c.Config.Org, appName)
	} else {
		endpoint = api.Routes.Path("StagingLogs", c.Config.Org, stageID)
	}
	webSocketConn, resp, err := websocket.DefaultDialer.Dial(
		fmt.Sprintf("%s/%s?%s", c.wsServerURL, endpoint, strings.Join(urlArgs, "&")), headers)
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("Failed to connect to websockets endpoint. Response was = %+v\nThe error is", resp))
	}

	done := make(chan bool)
	// When we get an interrupt, we close the websocket connection and we
	// we don't want to return an error in this case.
	connectionClosedByUs := false

	var wg sync.WaitGroup
	wg.Add(1)
	defer wg.Wait()
	go func() { // Closes the connection on "interrupt" or just stops on "done"
		defer wg.Done()
		for {
			select {
			case <-done: // Used by the other loop stop stop this go routine
				return
			case <-interrupt:
				// Used by the caller of this method to stop everything. We simply close
				// the connection here. This will make the loop below to stop and send us
				// a signal on "done" above. That will stop this go routine too.
				webSocketConn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Time{})
				connectionClosedByUs = true
				webSocketConn.Close()
			}
		}
	}()

	defer func() {
		done <- true // Stop the go routine when we return
	}()

	var logLine tailer.ContainerLogLine
	printer := logprinter.LogPrinter{Tmpl: logprinter.DefaultSingleNamespaceTemplate()}
	for {
		_, message, err := webSocketConn.ReadMessage()
		if err != nil {
			if connectionClosedByUs {
				return nil
			}
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				webSocketConn.Close()
				return nil
			}
			return err
		}
		err = json.Unmarshal(message, &logLine)
		if err != nil {
			return err
		}

		printer.Print(logprinter.Log{
			Message:       logLine.Message,
			Namespace:     logLine.Namespace,
			PodName:       logLine.PodName,
			ContainerName: logLine.ContainerName,
		}, c.ui.ProgressNote().Compact())
	}
}

// CreateOrg creates an Org in gitea
func (c *EpinioClient) CreateOrg(org string) error {
	log := c.Log.WithName("CreateOrg").WithValues("Organization", org)
	log.Info("start")
	defer log.Info("return")
	details := log.V(1) // NOTE: Increment of level, not absolute.

	c.ui.Note().
		WithStringValue("Name", org).
		Msg("Creating organization...")

	errorMsgs := validation.IsDNS1123Subdomain(org)
	if len(errorMsgs) > 0 {
		return fmt.Errorf("%s: %s", "org name incorrect", strings.Join(errorMsgs, "\n"))
	}

	err := retry.Do(
		func() error {
			details.Info("create org", "org", org)
			_, err := c.post(api.Routes.Path("Orgs"), fmt.Sprintf(`{ "name": "%s" }`, org))
			return err
		},
		retry.RetryIf(func(err error) bool {
			emsg := err.Error()
			details.Info("create error", "error", emsg)

			retry := strings.Contains(emsg, " x509: ") ||
				strings.Contains(emsg, "Gateway") ||
				(strings.Contains(emsg, "api/v1/orgs") &&
					strings.Contains(emsg, "i/o timeout"))

			details.Info("create error", "retry", retry)
			return retry
		}),
		retry.OnRetry(func(n uint, err error) {
			details.Info("create org retry", "n", n)
			c.ui.Note().Msgf("Retrying (%d/%d) after %s", n, duration.RetryMax, err.Error())
		}),
		retry.Delay(time.Second),
		retry.Attempts(duration.RetryMax),
	)

	if err != nil {
		return err
	}

	c.ui.Success().Msg("Organization created.")

	return nil
}

// DeleteOrg deletes an Org in gitea
func (c *EpinioClient) DeleteOrg(org string) error {
	log := c.Log.WithName("DeleteOrg").WithValues("Organization", org)
	log.Info("start")
	defer log.Info("return")

	c.ui.Note().
		WithStringValue("Name", org).
		Msg("Deleting organization...")

	_, err := c.delete(api.Routes.Path("OrgDelete", org))
	if err != nil {
		return err
	}

	c.ui.Success().Msg("Organization deleted.")

	return nil
}

// Delete removes the named application from the cluster
func (c *EpinioClient) Delete(ctx context.Context, appname string) error {
	log := c.Log.WithName("Delete").WithValues("Application", appname)
	log.Info("start")
	defer log.Info("return")

	c.ui.Note().
		WithStringValue("Name", appname).
		WithStringValue("Organization", c.Config.Org).
		Msg("Deleting application...")

	s := c.ui.Progressf("Deleting %s in %s", appname, c.Config.Org)
	defer s.Stop()

	jsonResponse, err := c.delete(api.Routes.Path("AppDelete", c.Config.Org, appname))
	if err != nil {
		return err
	}
	var response *models.ApplicationDeleteResponse
	if err := json.Unmarshal(jsonResponse, &response); err != nil {
		return err
	}

	unboundServices := response.UnboundServices
	if len(unboundServices) > 0 {
		s.Stop()

		sort.Strings(unboundServices)
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

	// TODO Create and use server endpoints. Maybe use existing
	// `Index`/Listing endpoint, either with parameter for
	// matching, or local matching.

	result := []string{}

	jsonResponse, err := c.get(api.Routes.Path("Orgs"))
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
	jsonResponse, err := c.get(api.Routes.Path("Orgs"))
	if err != nil {
		return err
	}

	var orgs []string
	if err := json.Unmarshal(jsonResponse, &orgs); err != nil {
		return err
	}

	sort.Strings(orgs)
	msg := c.ui.Success().WithTable("Name")

	for _, org := range orgs {
		msg = msg.WithTableRow(org)
	}

	msg.Msg("Epinio Organizations:")

	return nil
}

// Push pushes an app
// * validate
// * upload
// * stage
// * (tail logs)
// * wait for pipelinerun
// * wait for app
func (c *EpinioClient) Push(ctx context.Context, name, rev, source string, params PushParams) error {
	appRef := models.AppRef{Name: name, Org: c.Config.Org}
	log := c.Log.
		WithName("Push").
		WithValues("Name", appRef.Name,
			"Organization", appRef.Org,
			"Sources", source)
	log.Info("start")
	defer log.Info("return")
	details := log.V(1) // NOTE: Increment of level, not absolute. Visible via TRACE_LEVEL=2

	sourceToShow := source
	if rev != "" {
		sourceToShow = fmt.Sprintf("%s @ %s", sourceToShow, rev)
	}

	msg := c.ui.Note().
		WithStringValue("Name", appRef.Name).
		WithStringValue("Sources", sourceToShow).
		WithStringValue("Organization", appRef.Org)

	services := uniqueStrings(params.Services)

	if len(services) > 0 {
		sort.Strings(services)
		msg = msg.WithStringValue("Services:", strings.Join(services, ", "))
	}

	msg.Msg("About to push an application with given name and sources into the specified organization")

	c.ui.Exclamation().
		Timeout(duration.UserAbort()).
		Msg("Hit Enter to continue or Ctrl+C to abort (deployment will continue automatically in 5 seconds)")

	details.Info("validate app name")
	errorMsgs := validation.IsDNS1123Subdomain(appRef.Name)
	if len(errorMsgs) > 0 {
		return fmt.Errorf("%s: %s", "app name incorrect", strings.Join(errorMsgs, "\n"))
	}

	c.ui.Normal().Msg("Create the application resource ...")

	request := models.ApplicationCreateRequest{Name: appRef.Name}
	js, err := json.Marshal(request)
	if err != nil {
		return err
	}
	_, err = c.curlWithCustomErrorHandling(
		api.Routes.Path("AppCreate", appRef.Org), "POST", string(js), func(
			response *http.Response, bodyBytes []byte, err error) error {
			if response.StatusCode == http.StatusConflict {
				c.ui.Normal().Msg("Application exists, updating ...")
				return nil
			}
			return err
		},
	)
	if err != nil {
		return err
	}

	var gitRef *models.GitRef

	if rev == "" {
		c.ui.Normal().Msg("Collecting the application sources ...")

		tmpDir, tarball, err := collectSources(log, source)
		defer func() {
			if tmpDir != "" {
				_ = os.RemoveAll(tmpDir)
			}
		}()
		if err != nil {
			return err
		}

		c.ui.Normal().Msg("Uploading application code ...")

		details.Info("upload code")
		upload, err := c.uploadCode(appRef, tarball)
		if err != nil {
			return err
		}
		log.V(3).Info("upload response", "response", upload)

		gitRef = upload.Git
	} else {
		gitRef = &models.GitRef{
			URL:      source,
			Revision: rev,
		}
	}

	c.ui.Normal().Msg("Staging application ...")

	route, err := appDefaultRoute(ctx, appRef.Name)
	if err != nil {
		return errors.Wrap(err, "unable to determine default app route")
	}
	req := models.StageRequest{
		App:       appRef,
		Instances: params.Instances,
		Git:       gitRef,
		Route:     route,
	}
	details.Info("staging code", "Git", gitRef.Revision)
	stage, err := c.stageCode(req)
	if err != nil {
		return err
	}
	log.V(3).Info("stage response", "response", stage)

	details.Info("start tailing logs", "StageID", stage.Stage.ID)

	// Buffered because the go routine may no longer be listening when we try
	// to stop it. Stopping it should be a fire and forget. We have wg to wait
	// for the routine to be gone.
	stopChan := make(chan bool, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	defer wg.Wait()
	go func() {
		defer wg.Done()
		err := c.AppLogs(appRef.Name, stage.Stage.ID, true, stopChan)
		if err != nil {
			c.ui.Problem().Msg(fmt.Sprintf("failed to tail logs: %s", err.Error()))
		}
	}()

	details.Info("wait for pipelinerun", "StageID", stage.Stage.ID)
	err = c.waitForPipelineRun(ctx, appRef, stage.Stage.ID)
	if err != nil {
		stopChan <- true // Stop the printing go routine
		return errors.Wrap(err, "waiting for staging failed")
	}
	stopChan <- true // Stop the printing go routine

	details.Info("wait for app", "StageID", stage.Stage.ID)
	err = c.waitForApp(ctx, appRef, stage.Stage.ID)
	if err != nil {
		return errors.Wrap(err, "waiting for app failed")
	}

	// TODO : This services work should be moved into the stage
	// request, and server side.

	if len(services) > 0 {
		c.ui.Note().Msg("Binding Services")

		// Application is up, bind the services.
		// This will restart the application.
		// TODO: See #347 for future work

		request := models.BindRequest{
			Names: services,
		}

		js, err := json.Marshal(request)
		if err != nil {
			return err
		}

		b, err := c.post(api.Routes.Path("ServiceBindingCreate", appRef.Org, appRef.Name), string(js))
		if err != nil {
			return err
		}

		br := &models.BindResponse{}
		if err := json.Unmarshal(b, br); err != nil {
			return err
		}

		msg := c.ui.Note()
		text := "Done"
		if len(br.WasBound) > 0 {
			text = text + ", With Already Bound Services"
			msg = msg.WithTable("Name")

			for _, wasbound := range br.WasBound {
				msg = msg.WithTableRow(wasbound)
			}
		}

		msg.Msg(text)
	}

	c.ui.Success().
		WithStringValue("Name", appRef.Name).
		WithStringValue("Organization", appRef.Org).
		WithStringValue("Route", fmt.Sprintf("https://%s", route)).
		Msg("App is online.")

	return nil
}

func appDefaultRoute(ctx context.Context, name string) (string, error) {
	mainDomain, err := domain.MainDomain(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s.%s", name, mainDomain), nil
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

	// TODO: Validation of the org name removed. Proper validation
	// of the targeted org is done by all the other commands using
	// it anyway. If we really want it here and now, implement an
	// `org show` command and API, and then use that API for the
	// validation.

	details.Info("set config")
	c.Config.Org = org
	err := c.Config.Save()
	if err != nil {
		return errors.Wrap(err, "failed to save configuration")
	}

	c.ui.Success().Msg("Organization targeted.")

	return nil
}

func (c *EpinioClient) ServicesToApps(ctx context.Context, org string) (map[string]models.AppList, error) {
	// Determine apps bound to services
	// (inversion of services bound to apps)
	// Literally query apps in the org for their services and invert.

	var appsOf = map[string]models.AppList{}

	apps, err := application.List(ctx, c.Cluster, c.Config.Org)
	if err != nil {
		return nil, err
	}

	for _, app := range apps {
		w := application.NewWorkload(c.Cluster, app.AppRef())
		bound, err := w.Services(ctx)
		if err != nil {
			return nil, err
		}
		for _, bonded := range bound {
			bname := bonded.Name()
			if theapps, found := appsOf[bname]; found {
				appsOf[bname] = append(theapps, app)
			} else {
				appsOf[bname] = models.AppList{app}
			}
		}
	}

	return appsOf, nil
}

func (c *EpinioClient) get(endpoint string) ([]byte, error) {
	return c.curl(endpoint, "GET", "")
}

func (c *EpinioClient) post(endpoint string, data string) ([]byte, error) {
	return c.curl(endpoint, "POST", data)
}

func (c *EpinioClient) patch(endpoint string, data string) ([]byte, error) {
	return c.curl(endpoint, "PATCH", data)
}

func (c *EpinioClient) delete(endpoint string) ([]byte, error) {
	return c.curl(endpoint, "DELETE", "")
}

// upload the given path as param "file" in a multipart form
func (c *EpinioClient) upload(endpoint string, path string) ([]byte, error) {
	uri := fmt.Sprintf("%s/%s", c.serverURL, endpoint)

	// open the tarball
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.Wrap(err, "failed to open tarball")
	}
	defer file.Close()

	// create multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filepath.Base(file.Name()))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create multiform part")
	}

	_, err = io.Copy(part, file)
	if err != nil {
		return nil, errors.Wrap(err, "failed to write to multiform part")
	}

	err = writer.Close()
	if err != nil {
		return nil, errors.Wrap(err, "failed to close multiform")
	}

	// make the request
	request, err := http.NewRequest("POST", uri, body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to build request")
	}

	request.SetBasicAuth(c.Config.User, c.Config.Password)
	request.Header.Add("Content-Type", writer.FormDataContentType())

	response, err := (&http.Client{}).Do(request)
	if err != nil {
		return nil, errors.Wrap(err, "failed to POST to upload")
	}
	defer response.Body.Close()

	bodyBytes, _ := ioutil.ReadAll(response.Body)
	if response.StatusCode == http.StatusCreated {
		return bodyBytes, nil
	}

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server status code: %s\n%s", http.StatusText(response.StatusCode), string(bodyBytes))
	}

	// object was not created, but status was ok?
	return bodyBytes, nil
}

func (c *EpinioClient) curl(endpoint, method, requestBody string) ([]byte, error) {
	uri := fmt.Sprintf("%s/%s", c.serverURL, endpoint)
	c.Log.Info(fmt.Sprintf("%s %s", method, uri))
	c.Log.V(1).Info(requestBody)
	request, err := http.NewRequest(method, uri, strings.NewReader(requestBody))
	if err != nil {
		return []byte{}, err
	}

	request.SetBasicAuth(c.Config.User, c.Config.Password)

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

	request.SetBasicAuth(c.Config.User, c.Config.Password)

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

func uniqueStrings(stringSlice []string) []string {
	keys := make(map[string]bool)
	list := []string{}
	for _, entry := range stringSlice {
		if _, value := keys[entry]; !value {
			keys[entry] = true
			list = append(list, entry)
		}
	}
	return list
}

func getCredentials(log logr.Logger, ctx context.Context) (string, string, error) {
	cluster, err := kubernetes.GetCluster(ctx)
	if err != nil {
		return "", "", err
	}

	log.Info("got cluster")

	// Waiting for the secret is better than simply trying to get
	// it. This way we automatically handle the case where we try
	// to pull data from a secret still under construction by some
	// other part of the system.
	//
	// See assets/embedded-files/epinio/server.yaml for the
	// definition

	secret, err := cluster.WaitForSecret(ctx,
		deployments.EpinioDeploymentID,
		"epinio-api-auth-data",
		duration.ToServiceSecret(),
	)
	if err != nil {
		return "", "", errors.Wrap(err, "failed to get API auth secret")
	}

	log.Info("got secret", "secret", "epinio-api-auth-data")

	user := string(secret.Data["user"])
	pass := string(secret.Data["pass"])

	if user == "" || pass == "" {
		return "", "", errors.New("bad API auth secret, expected fields missing")
	}

	return user, pass, nil
}

func getCerts(ctx context.Context, log logr.Logger) (string, error) {
	// Save the  CA cert into the config. The regular client
	// will then extend the Cert pool with the same, so that it
	// can cerify the server cert.

	cluster, err := kubernetes.GetCluster(ctx)
	if err != nil {
		return "", err
	}

	log.Info("got cluster")

	// Waiting for the secret is better than simply trying to get
	// it. This way we automatically handle the case where we try
	// to pull data from a secret still under construction by some
	// other part of the system.

	// See the `auth.createCertificate` template for the created
	// Certs, and epinio.go `apply` for the call to
	// `auth.createCertificate`, which determines the secret's
	// name we are using here

	secret, err := cluster.WaitForSecret(ctx,
		deployments.EpinioDeploymentID,
		deployments.EpinioDeploymentID+"-tls",
		duration.ToServiceSecret(),
	)

	if err != nil {
		return "", errors.Wrap(err, "failed to get API CA cert secret")
	}

	log.Info("got secret", "secret", deployments.EpinioDeploymentID+"-tls")

	return string(secret.Data["ca.crt"]), nil
}
