package acceptance_test

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"regexp"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/codeskyblue/kexec"
	"github.com/epinio/epinio/helpers"
	"github.com/onsi/ginkgo/config"
	"github.com/pkg/errors"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestAcceptance(t *testing.T) {
	RegisterFailHandler(FailWithReport)
	RunSpecs(t, "Acceptance Suite")
}

var (
	nodeSuffix, nodeTmpDir string

	// serverURL is the URL of the epinio API server
	serverURL, websocketURL string
	registryMirrorName      = "epinio-acceptance-registry-mirror"
	epinioMagicDomain       = "omg.howdoi.website"
)

const (
	networkName          = "epinio-acceptance"
	registryMirrorEnv    = "EPINIO_REGISTRY_CONFIG"
	registryUsernameEnv  = "REGISTRY_USERNAME"
	registryPasswordEnv  = "REGISTRY_PASSWORD"
	epinioMagicDomainEnv = "EPINIO_MAGIC_DOMAIN"

	// skipCleanupPath is the path (relative to the test
	// directory) of a file which, when present causes the system
	// to not delete the test cluster after the tests are done.
	skipCleanupPath = "../tmp/skip_cleanup"

	// afterEachSleepPath is the path (relative to the test
	// directory) of a file which, when it, is readable, and
	// contains an integer number (*) causes the the system to
	// wait that many seconds after each test.
	//
	// (*) A number, i.e. just digits. __No trailing newline__
	afterEachSleepPath = "../tmp/after_each_sleep"

	epinioYAML = "../tmp/epinio.yaml"

	// skipEpinioPatch contains the name of the environment
	// variable which, when present and not empty causes system
	// startup to skip patching the epinio server pod. Best used
	// when the cluster of a previous run still exists
	// (s.a. skipCleanupPath).
	skipEpinioPatch = "EPINIO_SKIP_PATCH"

	// epinioUser and epinioPassword specify the API credentials
	// used during testing.
	epinioUser     = "test-user"
	epinioPassword = "secure-testing"
)

var _ = SynchronizedBeforeSuite(func() []byte {
	// Singleton setup. Run on node 1 before all

	fmt.Printf("I'm running on runner = %s\n", os.Getenv("HOSTNAME"))

	if d := os.Getenv(epinioMagicDomainEnv); d != "" {
		epinioMagicDomain = d
	}

	if os.Getenv(registryUsernameEnv) == "" || os.Getenv(registryPasswordEnv) == "" {
		fmt.Println("REGISTRY_USERNAME or REGISTRY_PASSWORD environment variables are empty. Pulling from dockerhub will be subject to rate limiting.")
	}

	if err := checkDependencies(); err != nil {
		panic("Missing dependencies: " + err.Error())
	}

	fmt.Printf("Compiling Epinio on node %d\n", config.GinkgoConfig.ParallelNode)
	buildEpinio()

	os.Setenv("EPINIO_BINARY_PATH", path.Join("dist", "epinio-linux-amd64"))
	os.Setenv("EPINIO_DONT_WAIT_FOR_DEPLOYMENT", "1")
	os.Setenv("EPINIO_CONFIG", epinioYAML)
	os.Setenv("SKIP_SSL_VERIFICATION", "true")

	if os.Getenv(registryUsernameEnv) != "" && os.Getenv(registryPasswordEnv) != "" {
		fmt.Printf("Creating image pull secret for Dockerhub on node %d\n", config.GinkgoConfig.ParallelNode)
		_, _ = helpers.Kubectl(fmt.Sprintf("create secret docker-registry regcred --docker-server=%s --docker-username=%s --docker-password=%s",
			"https://index.docker.io/v1/",
			os.Getenv(registryUsernameEnv),
			os.Getenv(registryPasswordEnv),
		))
	}

	ensureEpinio()

	if os.Getenv(skipEpinioPatch) == "" {
		// Patch Epinio deployment to inject the current binary
		fmt.Println("Patching Epinio deployment with test binary")
		out, err := RunProc("make patch-epinio-deployment", "..", false)
		Expect(err).ToNot(HaveOccurred(), out)
	}

	// Now create the default org which we skipped because it would fail before
	// patching.
	// NOTE: Unfortunately this prevents us from testing if the `install` command
	// really creates a default workspace. Needs a better solution that allows
	// install to do it's thing without needing the patch script to run first.
	// Eventually is used to retry in case the rollout of the patched deployment
	// is not completely done yet.
	fmt.Println("Ensure default workspace exists")
	Eventually(func() string {
		out, err := RunProc("../dist/epinio-linux-amd64 org create workspace", "", false)
		if err != nil {
			if exists, err := regexp.Match(`Organization 'workspace' already exists`, []byte(out)); err == nil && exists {
				return ""
			}
			return errors.Wrap(err, out).Error()
		}
		return ""
	}, "1m").Should(BeEmpty())

	fmt.Println("Setup cluster services")
	setupInClusterServices()
	out, err := helpers.Kubectl(`get pods -n minibroker --selector=app=minibroker-minibroker`)
	Expect(err).ToNot(HaveOccurred(), out)
	Expect(out).To(MatchRegexp(`minibroker.*2/2.*Running`))

	fmt.Println("Setup google")
	setupGoogleServices()

	fmt.Println("SynchronizedBeforeSuite is done, checking Epinio info endpoint")
	expectGoodInstallation()

	return []byte(strconv.Itoa(int(time.Now().Unix())))
}, func(randomSuffix []byte) {
	var err error

	nodeSuffix = fmt.Sprintf("%d-%s",
		config.GinkgoConfig.ParallelNode, string(randomSuffix))
	nodeTmpDir, err = ioutil.TempDir("", "epinio-"+nodeSuffix)
	if err != nil {
		panic("Could not create temp dir: " + err.Error())
	}

	Expect(os.Getenv("KUBECONFIG")).ToNot(BeEmpty(), "KUBECONFIG environment variable should not be empty")

	out, err := copyEpinio()
	Expect(err).ToNot(HaveOccurred(), out)

	os.Setenv("EPINIO_CONFIG", nodeTmpDir+"/epinio.yaml")

	// Get config from the installation (API credentials)
	out, err = RunProc(fmt.Sprintf("cp %s %s/epinio.yaml", epinioYAML, nodeTmpDir), "", false)
	Expect(err).ToNot(HaveOccurred(), out)

	out, err = Epinio("target workspace", nodeTmpDir)
	Expect(err).ToNot(HaveOccurred(), out)

	out, err = RunProc("kubectl get ingress -n epinio epinio -o=jsonpath='{.spec.rules[0].host}'", "..", false)
	Expect(err).ToNot(HaveOccurred(), out)

	serverURL = "https://" + out
	websocketURL = "wss://" + out
})

var _ = SynchronizedAfterSuite(func() {
	if !skipCleanup() {
		fmt.Printf("Deleting tmpdir on node %d\n", config.GinkgoConfig.ParallelNode)
		deleteTmpDir()
	}
}, func() { // Runs only on one node after all are done
	if skipCleanup() {
		fmt.Printf("Found '%s', skipping all cleanup", skipCleanupPath)
	} else {
		// Delete left-overs no matter what
		defer func() { _, _ = cleanupTmp() }()
	}
})

var _ = AfterEach(func() {
	if _, err := os.Stat(afterEachSleepPath); err == nil {
		if data, err := ioutil.ReadFile(afterEachSleepPath); err == nil {
			if s, err := strconv.Atoi(string(data)); err == nil {
				t := time.Duration(s) * time.Second
				fmt.Printf("Found '%s', sleeping for '%s'", afterEachSleepPath, t)
				time.Sleep(t)
			}
		}
	}
})

// skipCleanup returns true if the file exists, false if some error occurred
// while checking
func skipCleanup() bool {
	_, err := os.Stat(skipCleanupPath)
	return err == nil
}

func ensureEpinio() {
	out, err := helpers.Kubectl(`get pods -n epinio --selector=app.kubernetes.io/name=epinio-server`)
	if err == nil {
		running, err := regexp.Match(`epinio-server.*Running`, []byte(out))
		if err != nil {
			return
		}
		if running {
			return
		}
	}
	fmt.Println("Installing Epinio")

	// Installing linkerd and ingress separate from the main
	// pieces.  Ensures that the main install command invokes and
	// passes the presence checks for linkerd and traefik.
	out, err = RunProc(
		"../dist/epinio-linux-amd64 install-ingress",
		"", false)
	Expect(err).ToNot(HaveOccurred(), out)

	ingressIP, err := RunProc("kubectl get service  traefik -n traefik -o jsonpath={.status.loadBalancer.ingress[*].ip}", nodeTmpDir, false)
	Expect(err).ToNot(HaveOccurred())
	Expect(out).To(MatchRegexp(fmt.Sprintf("Traefik Ingress info:.*%s.*", ingressIP)))

	domainSetting := ""
	if domain := os.Getenv("EPINIO_SYSTEM_DOMAIN"); domain != "" {
		domainSetting = fmt.Sprintf(" --system-domain %s", domain)
	}

	// Allow the installation to continue by not trying to create the default org
	// before we patch.
	out, err = RunProc(
		fmt.Sprintf("../dist/epinio-linux-amd64 install --skip-default-org --user %s --password %s %s", epinioUser, epinioPassword, domainSetting),
		"", false)
	Expect(err).ToNot(HaveOccurred(), out)
}

func deleteTmpDir() {
	err := os.RemoveAll(nodeTmpDir)
	if err != nil {
		panic(fmt.Sprintf("Failed deleting temp dir %s: %s\n",
			nodeTmpDir, err.Error()))
	}
}

func GetProc(command string, dir string) (*kexec.KCommand, error) {
	var commandDir string
	var err error

	if dir == "" {
		commandDir, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	} else {
		commandDir = dir
	}

	p := kexec.CommandString(command)
	p.Dir = commandDir

	return p, nil
}

func RunProc(cmd, dir string, toStdout bool) (string, error) {
	p, err := GetProc(cmd, dir)
	if err != nil {
		return "", err
	}

	var b bytes.Buffer
	if toStdout {
		p.Stdout = io.MultiWriter(os.Stdout, &b)
		p.Stderr = io.MultiWriter(os.Stderr, &b)
	} else {
		p.Stdout = &b
		p.Stderr = &b
	}

	if err := p.Run(); err != nil {
		return b.String(), err
	}

	err = p.Wait()
	return b.String(), err
}

func buildEpinio() {
	output, err := RunProc("make", "..", false)
	if err != nil {
		panic(fmt.Sprintf("Couldn't build Epinio: %s\n %s\n"+err.Error(), output))
	}
}

func copyEpinio() (string, error) {
	binaryPath := "dist/epinio-" + runtime.GOOS + "-" + runtime.GOARCH
	return RunProc("cp "+binaryPath+" "+nodeTmpDir+"/epinio", "..", false)
}

// Remove all tmp directories from /tmp/epinio-* . Test should try to cleanup
// after themselves but that sometimes doesn't happen, either because we forgot
// the cleanup code or because the test failed before that happened.
// NOTE: This code will create problems if more than one acceptance_suite_test.go
// is run in parallel (e.g. two PRs on one worker). However we keep it as an
// extra measure.
func cleanupTmp() (string, error) {
	return RunProc("rm -rf /tmp/epinio-*", "", true)
}

// Epinio invokes the `epinio` binary, running the specified command.
// It returns the command output and/or error.
// dir parameter defines the directory from which the command should be run.
// It defaults to the current dir if left empty.
func Epinio(command string, dir string) (string, error) {
	cmd := fmt.Sprintf(nodeTmpDir+"/epinio %s", command)
	return RunProc(cmd, dir, false)
}

func checkDependencies() error {
	ok := true

	dependencies := []struct {
		CommandName string
	}{
		{CommandName: "wget"},
		{CommandName: "tar"},
	}

	for _, dependency := range dependencies {
		_, err := exec.LookPath(dependency.CommandName)
		if err != nil {
			fmt.Printf("Not found: %s\n", dependency.CommandName)
			ok = false
		}
	}

	if ok {
		return nil
	}

	return errors.New("Please check your PATH, some of our dependencies were not found")
}

func FailWithReport(message string, callerSkip ...int) {
	// NOTE: Use something like the following if you need to debug failed tests
	// fmt.Println("\nA test failed. You may find the following information useful for debugging:")
	// fmt.Println("The cluster pods: ")
	// out, err := helpers.Kubectl("get pods --all-namespaces")
	// if err != nil {
	// 	fmt.Print(err.Error())
	// } else {
	// 	fmt.Print(out)
	// }

	// Ensures the correct line numbers are reported
	Fail(message, callerSkip[0]+1)
}

func expectGoodInstallation() {
	info, err := RunProc("../dist/epinio-linux-amd64 info", "", false)
	Expect(err).ToNot(HaveOccurred())
	Expect(info).To(MatchRegexp("Platform: k3s"))
	Expect(info).To(MatchRegexp("Kubernetes Version: v1.20"))
	Expect(info).To(MatchRegexp("Gitea Version: unavailable"))
}

func setupGoogleServices() {
	serviceAccountJSON, err := helpers.CreateTmpFile(`
				{
					"type": "service_account",
					"project_id": "myproject",
					"private_key_id": "somekeyid",
					"private_key": "someprivatekey",
					"client_email": "client@example.com",
					"client_id": "clientid",
					"auth_uri": "https://accounts.google.com/o/oauth2/auth",
					"token_uri": "https://oauth2.googleapis.com/token",
					"auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
					"client_x509_cert_url": "https://www.googleapis.com/robot/v1/metadata/x509/client%40example.com"
				}
			`)
	Expect(err).ToNot(HaveOccurred(), serviceAccountJSON)

	defer os.Remove(serviceAccountJSON)

	out, err := RunProc("../dist/epinio-linux-amd64 enable services-google --service-account-json "+serviceAccountJSON, "", false)
	Expect(err).ToNot(HaveOccurred(), out)

	out, err = helpers.Kubectl(`get pods -n google-service-broker --selector=app.kubernetes.io/name=gcp-service-broker`)
	Expect(err).ToNot(HaveOccurred(), out)
	Expect(out).To(MatchRegexp(`google-service-broker-gcp-service-broker.*2/2.*Running`))
}
