package docs

//go:generate swagger generate spec

import (
	"github.com/suse/carrier/internal/application"
)

// swagger:route GET /api/v1/orgs/{org}/applications application listApplications
// List deployed applications
// responses:
//   200: listApplicationsResponse

// List of applications
// swagger:response listApplicationsResponse
type listApplicationsResponseWrapper struct {
	// in:body
	Body application.Application
}

// swagger:parameters listApplications
type listApplicationsParamWrapper struct {
	// Parameters for list applications
	Org string
}

// swagger:route GET /api/v1/orgs/{org}/applications/:app application showApplication
// Show info about an application
// responses:
//   200: showApplicationResponse

// This text will appear as description of your response body.
// swagger:response showApplicationResponse
type showApplicationResponseWrapper struct {
	// in:body
	Body application.Application
}

// swagger:parameters showApplication
type showApplicationParamWrapper struct {
	// Parameters for show application
	Org string
	App string
}
