package v1

import (
	"encoding/json"
	"io/ioutil"
	"net/http"

	"github.com/epinio/epinio/helpers/kubernetes"
	"github.com/epinio/epinio/internal/api/v1/models"
	"github.com/epinio/epinio/internal/application"
	"github.com/epinio/epinio/internal/interfaces"
	"github.com/epinio/epinio/internal/organizations"
	"github.com/epinio/epinio/internal/services"
	"github.com/julienschmidt/httprouter"
	"github.com/pkg/errors"
)

type ServicebindingsController struct {
}

// General behaviour: Internal errors (5xx) abort an action.
// Non-internal errors and warnings may be reported with it,
// however always after it. IOW an internal error is always
// the first element when reporting more than one error.

func (hc ServicebindingsController) Create(w http.ResponseWriter, r *http.Request) APIErrors {
	params := httprouter.ParamsFromContext(r.Context())
	org := params.ByName("org")
	appName := params.ByName("app")

	defer r.Body.Close()
	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return APIErrors{InternalError(err)}
	}

	var bindRequest models.BindRequest
	err = json.Unmarshal(bodyBytes, &bindRequest)
	if err != nil {
		return APIErrors{BadRequest(err)}
	}

	if len(bindRequest.Names) == 0 {
		err := errors.New("Cannot bind service without names")
		return APIErrors{BadRequest(err)}
	}

	for _, serviceName := range bindRequest.Names {
		if serviceName == "" {
			err := errors.New("Cannot bind service with empty name")
			return APIErrors{BadRequest(err)}
		}
	}

	cluster, err := kubernetes.GetCluster()
	if err != nil {
		return APIErrors{InternalError(err)}
	}

	exists, err := organizations.Exists(cluster, org)
	if err != nil {
		return APIErrors{InternalError(err)}
	}
	if !exists {
		return APIErrors{OrgIsNotKnown(org)}
	}

	app, err := application.Lookup(cluster, org, appName)
	if err != nil {
		return APIErrors{InternalError(err)}
	}
	if app == nil {
		return APIErrors{AppIsNotKnown(appName)}
	}

	// From here on out we collect errors and warnings per
	// service, to report as much as possible while also applying
	// as much as possible. IOW even when errors are reported it
	// is possible for some of the services to be properly bound.

	var theServices interfaces.ServiceList
	var theIssues APIErrors

	for _, serviceName := range bindRequest.Names {
		service, err := services.Lookup(cluster, org, serviceName)
		if err != nil {
			if err.Error() == "service not found" {
				theIssues = append(theIssues, ServiceIsNotKnown(serviceName))
				continue
			}

			return append(APIErrors{InternalError(err)}, theIssues...)
		}

		theServices = append(theServices, service)
	}

	for _, service := range theServices {
		err = app.Bind(service)
		if err != nil {
			if err.Error() == "service already bound" {
				theIssues = append(theIssues, ServiceAlreadyBound(service.Name()))
				continue
			}

			return append(APIErrors{InternalError(err)}, theIssues...)
		}
	}

	if len(theIssues) > 0 {
		return theIssues
	}

	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write([]byte{})
	if err != nil {
		return append(APIErrors{InternalError(err)}, theIssues...)
	}

	return nil
}

func (hc ServicebindingsController) Delete(w http.ResponseWriter, r *http.Request) APIErrors {
	params := httprouter.ParamsFromContext(r.Context())
	org := params.ByName("org")
	appName := params.ByName("app")
	serviceName := params.ByName("service")

	cluster, err := kubernetes.GetCluster()
	if err != nil {
		return APIErrors{InternalError(err)}
	}

	exists, err := organizations.Exists(cluster, org)
	if err != nil {
		return APIErrors{InternalError(err)}
	}
	if !exists {
		return APIErrors{OrgIsNotKnown(org)}
	}

	app, err := application.Lookup(cluster, org, appName)
	if err != nil {
		return APIErrors{InternalError(err)}
	}
	if app == nil {
		return APIErrors{AppIsNotKnown(appName)}
	}

	service, err := services.Lookup(cluster, org, serviceName)
	if err != nil && err.Error() == "service not found" {
		return APIErrors{ServiceIsNotKnown(serviceName)}
	}
	if err != nil {
		return APIErrors{InternalError(err)}
	}

	err = app.Unbind(service)
	if err != nil && err.Error() == "service is not bound to the application" {
		return APIErrors{ServiceIsNotBound(serviceName)}
	}
	if err != nil {
		return APIErrors{InternalError(err)}
	}

	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write([]byte{})
	if err != nil {
		return APIErrors{InternalError(err)}
	}

	return nil
}
