package v1

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"

	"github.com/epinio/epinio/helpers/kubernetes"
	"github.com/epinio/epinio/internal/cli/clients/gitea"
	"github.com/epinio/epinio/internal/organizations"
)

type OrganizationsController struct {
}

// Index return a list of all Epinio orgs
// An Epinio org is nothing but a kubernetes namespace which has a special
// Label (Look at the code to see which).
func (oc OrganizationsController) Index(w http.ResponseWriter, r *http.Request) APIErrors {
	cluster, err := kubernetes.GetCluster()
	if err != nil {
		return APIErrors{InternalError(err)}
	}

	orgList, err := organizations.List(cluster)
	if err != nil {
		return APIErrors{InternalError(err)}
	}

	orgNames := []string{}
	for _, org := range orgList {
		orgNames = append(orgNames, org.Name)
	}

	js, err := json.Marshal(orgNames)
	if err != nil {
		return APIErrors{InternalError(err)}
	}
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(js)
	if err != nil {
		return APIErrors{InternalError(err)}
	}

	return nil
}

func (oc OrganizationsController) Create(w http.ResponseWriter, r *http.Request) APIErrors {
	gitea, err := gitea.New()
	if err != nil {
		return APIErrors{InternalError(err)}
	}

	cluster, err := kubernetes.GetCluster()
	if err != nil {
		return APIErrors{InternalError(err)}
	}

	defer r.Body.Close()
	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return APIErrors{InternalError(err)}
	}

	// map ~ json oject / Required key: name
	var parts map[string]string
	err = json.Unmarshal(bodyBytes, &parts)
	if err != nil {
		return APIErrors{BadRequest(err)}
	}

	org, ok := parts["name"]
	if !ok {
		err := errors.New("Name of organization to create not found")
		return APIErrors{BadRequest(err)}
	}

	exists, err := organizations.Exists(cluster, org)
	if err != nil {
		return APIErrors{InternalError(err)}
	}
	if exists {
		return APIErrors{OrgAlreadyKnown(org)}
	}

	err = organizations.Create(r.Context(), cluster, gitea, org)
	if err != nil {
		return APIErrors{InternalError(err)}
	}

	w.WriteHeader(http.StatusCreated)
	w.Write([]byte{})

	return nil
}
