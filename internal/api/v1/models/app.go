package models

const (
	EpinioStageIDLabel = "epinio.suse.org/stage-id"
)

// App has all the app properties, like the routes and stage ID.
// It is used in the CLI and  API responses.
type App struct {
	Active        bool     `json:"active,omitempty"`
	StageID       string   `json:"stage_id,omitempty"`
	Name          string   `json:"name,omitempty"`
	Organization  string   `json:"organization,omitempty"`
	Status        string   `json:"status,omitempty"`
	Routes        []string `json:"routes,omitempty"`
	BoundServices []string `json:"bound_services,omitempty"`
}

// NewApp returns a new app for name and org
func NewApp(name string, org string) *App {
	return &App{Name: name, Organization: org}
}

// AppRef returns a reference to the app (name, org)
func (a *App) AppRef() AppRef {
	return NewAppRef(a.Name, a.Organization)
}

type AppList []App

// Implement the Sort interface for application slices

func (al AppList) Len() int {
	return len(al)
}

func (al AppList) Swap(i, j int) {
	al[i], al[j] = al[j], al[i]
}

func (al AppList) Less(i, j int) bool {
	return al[i].Name < al[j].Name
}

// AppRef references an App by name and org
type AppRef struct {
	Name string `json:"name"`
	Org  string `json:"org"`
}

// NewAppRef returns a new reference to an app
func NewAppRef(name string, org string) AppRef {
	return AppRef{Name: name, Org: org}
}

// App returns an fresh app model for the reference
func (ar *AppRef) App() *App {
	return NewApp(ar.Name, ar.Org)
}

// StageRef references a tekton staging run by ID, currently randomly generated
// for each POST to the staging endpoint
type StageRef struct {
	ID string `json:"id,omitempty"`
}

// NewStage returns a new reference to a staging run
func NewStage(id string) StageRef {
	return StageRef{id}
}

// ImageRef references an upload
type ImageRef struct {
	ID string `json:"id,omitempty"`
}

// NewImage returns a new image ref for the given ID
func NewImage(id string) ImageRef {
	return ImageRef{id}
}

// GitRef describes a git commit in a repo
type GitRef struct {
	Revision string `json:"revision"`
	URL      string `json:"url"`
}

// DockerRef describes a git commit in a repo
type DockerRef struct {
	URL string `json:"url"`
}
