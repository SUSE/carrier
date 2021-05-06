package v1

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/epinio/epinio/helpers/kubernetes"
	"github.com/epinio/epinio/internal/api/v1/models"
	"github.com/epinio/epinio/internal/application"
	"github.com/epinio/epinio/internal/organizations"
	"github.com/epinio/epinio/internal/services"
	"github.com/julienschmidt/httprouter"
)

type ServicesController struct {
}

func (sc ServicesController) Show(w http.ResponseWriter, r *http.Request) []APIError {
	params := httprouter.ParamsFromContext(r.Context())
	org := params.ByName("org")
	serviceName := params.ByName("service")

	cluster, err := kubernetes.GetCluster()
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}

	exists, err := organizations.Exists(cluster, org)
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}
	if !exists {
		return []APIError{
			NewAPIError(fmt.Sprintf("Organization '%s' does not exist", org), "", http.StatusNotFound),
		}
	}

	service, err := services.Lookup(cluster, org, serviceName)
	if err != nil {
		if err.Error() == "service not found" {
			return []APIError{
				NewAPIError(fmt.Sprintf("Service '%s' does not exist", serviceName), "", http.StatusNotFound),
			}
		}
		if err != nil {
			return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
		}
	}

	status, err := service.Status()
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}
	serviceDetails, err := service.Details()
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}

	responseData := map[string]string{
		"Status": status,
	}
	for key, value := range serviceDetails {
		responseData[key] = value
	}

	js, err := json.Marshal(responseData)
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(js)
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}

	return []APIError{}
}

func (sc ServicesController) Index(w http.ResponseWriter, r *http.Request) []APIError {
	params := httprouter.ParamsFromContext(r.Context())
	org := params.ByName("org")

	cluster, err := kubernetes.GetCluster()
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}

	exists, err := organizations.Exists(cluster, org)
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}
	if !exists {
		return []APIError{
			NewAPIError(fmt.Sprintf("Organization '%s' does not exist", org), "", http.StatusNotFound),
		}
	}

	orgServices, err := services.List(cluster, org)
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}

	appsOf, err := servicesToApps(cluster, org)
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}

	var responseData models.ServiceResponseList

	for _, service := range orgServices {
		var appNames []string

		for _, app := range appsOf[service.Name()] {
			appNames = append(appNames, app.Name)
		}
		responseData = append(responseData, models.ServiceResponse{
			Name:      service.Name(),
			BoundApps: appNames,
		})
	}

	js, err := json.Marshal(responseData)
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(js)
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}

	return []APIError{}
}

func (sc ServicesController) CreateCustom(w http.ResponseWriter, r *http.Request) []APIError {
	params := httprouter.ParamsFromContext(r.Context())
	org := params.ByName("org")

	defer r.Body.Close()
	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}

	var createRequest models.CustomCreateRequest
	err = json.Unmarshal(bodyBytes, &createRequest)
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusBadRequest)}
	}

	if createRequest.Name == "" {
		return []APIError{
			NewAPIError("Cannot create custom service without a name", "", http.StatusBadRequest),
		}
	}

	if len(createRequest.Data) < 1 {
		return []APIError{
			NewAPIError("Cannot create custom service without data", "", http.StatusBadRequest),
		}
	}

	cluster, err := kubernetes.GetCluster()
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}

	exists, err := organizations.Exists(cluster, org)
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}
	if !exists {
		return []APIError{
			NewAPIError(fmt.Sprintf("Organization '%s' does not exist", org), "", http.StatusNotFound),
		}
	}

	// Verify that the requested name is not yet used by a different service.
	_, err = services.Lookup(cluster, org, createRequest.Name)
	if err == nil {
		// no error, service is found, conflict
		return []APIError{
			NewAPIError(fmt.Sprintf("Service '%s' already exists", createRequest.Name), "", http.StatusConflict),
		}
	}
	if err != nil && err.Error() != "service not found" {
		// some internal error
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}
	// any error here is `service not found`, and we can continue

	// Create the new service. At last.
	_, err = services.CreateCustomService(cluster, createRequest.Name, org, createRequest.Data)
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}

	w.WriteHeader(http.StatusCreated)
	_, err = w.Write([]byte{})
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}

	return []APIError{}
}

func (sc ServicesController) Create(w http.ResponseWriter, r *http.Request) []APIError {
	params := httprouter.ParamsFromContext(r.Context())
	org := params.ByName("org")

	cluster, err := kubernetes.GetCluster()
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}

	defer r.Body.Close()
	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}

	var createRequest models.CatalogCreateRequest
	err = json.Unmarshal(bodyBytes, &createRequest)
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusBadRequest)}
	}

	if createRequest.Name == "" {
		return []APIError{NewAPIError("Cannot create service without a name", "", http.StatusBadRequest)}
	}

	if createRequest.Class == "" {
		return []APIError{NewAPIError("Cannot create service without a service class", "", http.StatusBadRequest)}
	}

	if createRequest.Plan == "" {
		return []APIError{NewAPIError("Cannot create service without a service plan", "", http.StatusBadRequest)}
	}

	exists, err := organizations.Exists(cluster, org)
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}
	if !exists {
		return []APIError{
			NewAPIError(fmt.Sprintf("Organization '%s' does not exist", org), "", http.StatusNotFound),
		}
	}

	// Verify that the requested name is not yet used by a different service.
	_, err = services.Lookup(cluster, org, createRequest.Name)
	if err == nil {
		// no error, service is found, conflict
		return []APIError{
			NewAPIError(fmt.Sprintf("Service '%s' already exists", createRequest.Name), "", http.StatusConflict),
		}
	}
	if err != nil && err.Error() != "service not found" {
		// some internal error
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}
	// any error here is `service not found`, and we can continue

	// Verify that the requested class is supported
	serviceClass, err := services.ClassLookup(cluster, createRequest.Class)
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}
	if serviceClass == nil {
		return []APIError{NewAPIError(
			fmt.Sprintf("Service class '%s' does not exist", createRequest.Class), "", http.StatusNotFound),
		}
	}

	// Verify that the requested plan is supported by the class.
	servicePlan, err := serviceClass.LookupPlan(createRequest.Plan)
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}

	if servicePlan == nil {
		return []APIError{NewAPIError(
			fmt.Sprintf("Service plan '%s' does not exist for class '%s'", createRequest.Plan, createRequest.Class),
			"", http.StatusNotFound),
		}
	}

	// Create the new service. At last.
	service, err := services.CreateCatalogService(cluster, createRequest.Name, org,
		createRequest.Class, createRequest.Plan, createRequest.Data)
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}

	// Wait for service to be fully provisioned, if requested
	if createRequest.WaitForProvision {
		err := service.WaitForProvision()
		if err != nil {
			return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
		}
	}

	w.WriteHeader(http.StatusCreated)
	_, err = w.Write([]byte{})
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}

	return []APIError{}
}

func (sc ServicesController) Delete(w http.ResponseWriter, r *http.Request) []APIError {
	params := httprouter.ParamsFromContext(r.Context())
	org := params.ByName("org")
	serviceName := params.ByName("service")

	defer r.Body.Close()
	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}

	var deleteRequest models.DeleteRequest
	err = json.Unmarshal(bodyBytes, &deleteRequest)
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusBadRequest)}
	}

	cluster, err := kubernetes.GetCluster()
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}

	exists, err := organizations.Exists(cluster, org)
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}
	if !exists {
		return []APIError{
			NewAPIError(fmt.Sprintf("Organization '%s' does not exist", org), "", http.StatusNotFound),
		}
	}

	service, err := services.Lookup(cluster, org, serviceName)
	if err != nil && err.Error() == "service not found" {
		return []APIError{
			NewAPIError(fmt.Sprintf("service '%s' not found", serviceName), "", http.StatusNotFound),
		}
	}
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}

	// Verify that the service is unbound. IOW not bound to any application.
	// If it is, and automatic unbind was requested, do that.
	// Without automatic unbind such applications are reported as error.

	boundAppNames := []string{}
	appsOf, err := servicesToApps(cluster, org)
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}
	if boundApps, found := appsOf[service.Name()]; found {
		for _, app := range boundApps {
			boundAppNames = append(boundAppNames, app.Name)
		}

		if !deleteRequest.Unbind {
			return []APIError{
				NewAPIError("bound applications exist", strings.Join(boundAppNames, ","), http.StatusBadRequest),
			}
		}

		for _, app := range boundApps {
			err = app.Unbind(service)
			if err != nil {
				return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
			}
		}
	}

	// Everything looks to be ok. Delete.

	err = service.Delete()
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}

	js, err := json.Marshal(models.DeleteResponse{BoundApps: boundAppNames})
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}

	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(js)
	if err != nil {
		return []APIError{NewAPIError(err.Error(), "", http.StatusInternalServerError)}
	}

	return []APIError{}
}

func servicesToApps(cluster *kubernetes.Cluster, org string) (map[string]application.ApplicationList, error) {
	// Determine apps bound to services
	// (inversion of services bound to apps)
	// Literally query apps in the org for their services and invert.

	var appsOf = map[string]application.ApplicationList{}

	apps, err := application.List(cluster, org)
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
