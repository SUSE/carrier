// Package v1 is the implementation of Epinio's API v1
package v1

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/epinio/epinio/helpers/routes"
	"github.com/julienschmidt/httprouter"
)

const v = "/api/v1"

// jsonResponse writes the response struct as JSON to the writer
func jsonResponse(w http.ResponseWriter, response interface{}) error {
	w.Header().Set("Content-Type", "application/json")
	js, err := json.Marshal(response)
	if err != nil {
		return err
	}
	_, err = w.Write(js)
	return err
}

func jsonErrorResponse(w http.ResponseWriter, responseErrors ...APIError) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	response := ErrorResponse{Errors: responseErrors}
	js, marshalErr := json.Marshal(response)
	if marshalErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintln(w, marshalErr.Error())
		return
	}

	w.WriteHeader(responseErrors[0].Status)
	fmt.Fprintln(w, string(js))
}

func errorHandler(action APIActionFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if errors := action(w, r); len(errors) > 0 {
			jsonErrorResponse(w, errors...)
		}
	}
}

func get(path string, h http.HandlerFunc) routes.Route {
	return routes.NewRoute("GET", v+path, h)
}

func post(path string, h http.HandlerFunc) routes.Route {
	return routes.NewRoute("POST", v+path, h)
}

func delete(path string, h http.HandlerFunc) routes.Route {
	return routes.NewRoute("DELETE", v+path, h)
}

func patch(path string, h http.HandlerFunc) routes.Route {
	return routes.NewRoute("PATCH", v+path, h)
}

var Routes = routes.NamedRoutes{
	"Info":      get("/info", errorHandler(InfoController{}.Info)),
	"Apps":      get("/orgs/:org/applications", errorHandler(ApplicationsController{}.Index)),
	"AppShow":   get("/orgs/:org/applications/:app", errorHandler(ApplicationsController{}.Show)),
	"AppLogs":   get("/orgs/:org/applications/:app/logs", ApplicationsController{}.Logs),
	"AppDelete": delete("/orgs/:org/applications/:app", errorHandler(ApplicationsController{}.Delete)),
	"AppUpload": post("/orgs/:org/applications/:app/store", errorHandler(ApplicationsController{}.Upload)),
	"AppStage":  post("/orgs/:org/applications/:app/stage", errorHandler(ApplicationsController{}.Stage)),
	"AppUpdate": patch("/orgs/:org/applications/:app", errorHandler(ApplicationsController{}.Update)),

	// Bind and unbind services to/from applications, by means of servicebindings in applications
	"ServiceBindingCreate": post("/orgs/:org/applications/:app/servicebindings",
		errorHandler(ServicebindingsController{}.Create)),
	"ServiceBindingDelete": delete("/orgs/:org/applications/:app/servicebindings/:service",
		errorHandler(ServicebindingsController{}.Delete)),

	// List, create, show and delete organizations
	"Orgs":      get("/orgs", errorHandler(OrganizationsController{}.Index)),
	"OrgCreate": post("/orgs", errorHandler(OrganizationsController{}.Create)),
	"OrgDelete": delete("/orgs/:org", errorHandler(OrganizationsController{}.Delete)),

	// List, show, create and delete services, catalog and custom
	"Services":            get("/orgs/:org/services", errorHandler(ServicesController{}.Index)),
	"ServiceShow":         get("/orgs/:org/services/:service", errorHandler(ServicesController{}.Show)),
	"ServiceCreate":       post("/orgs/:org/services", errorHandler(ServicesController{}.Create)),
	"ServiceCreateCustom": post("/orgs/:org/custom-services", errorHandler(ServicesController{}.CreateCustom)),
	"ServiceDelete":       delete("/orgs/:org/services/:service", errorHandler(ServicesController{}.Delete)),

	// list service classes and plans (of catalog services)
	"ServiceClasses": get("/serviceclasses", errorHandler(ServiceClassesController{}.Index)),
	"ServicePlans":   get("/serviceclasses/:serviceclass/serviceplans", errorHandler(ServicePlansController{}.Index)),
}

func Router() *httprouter.Router {
	router := httprouter.New()

	for _, r := range Routes {
		router.HandlerFunc(r.Method, r.Path, r.Handler)
	}

	router.NotFound = http.NotFoundHandler()

	return router
}
