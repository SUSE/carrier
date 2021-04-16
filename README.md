# Epinio

Opinionated platform that runs on Kubernetes, that takes you from App to URL in one step.

![CI](https://github.com/epinio/epinio/workflows/CI/badge.svg)

<img src="./docs/epinio.png" width="50%" height="50%">

## What

Epinio makes it easy for developers to deploy their applications to Kubernetes. Easy means:

- No previous experience with Kubernetes is required
- No steep learning curve
- Quick local setup with zero configuration
- Deploying to production similar to development

## Why

Kubernetes is becoming the de-facto standard for container orchestration.
Developers may want to use Kubernetes for all the benefits it provides or may
have to do so because that's what their Ops team has chosen. Whatever the case,
using Kubernetes is not simple. It has a steep learning curve and doing it right
is a full time job. Developers should spend their time working on their applications,
not doing operations.

Epinio is adding the needed abstractions and intelligence to allow Developers
to use Kubernetes as a PaaS (Platform as a Service).

## Principles

Epinio's development is governed by the following principles:

- must fit in less than 4GB of RAM
- must install in less than 5 minutes when images are warm
- must install with an one-line command and zero config
- must completely uninstall and leave the cluster in its previous state with an one-line command
- must work on local clusters (edge friendly)

### Guidelines (Soft Principles)

- When possible, prefer:
  - components that are written in go
  - Kubernetes primitives over custom resources
  - Well known components with active community over custom code
- all acceptance tests should run in less than 10 minutes
- all tests should be able to run on the minimal cluster

## Quick start

Follow the instructions here to get started with Epinio.

### Install dependencies

- `git`: Installation method depends on your OS (https://git-scm.com/)
- `kubectl`: Follow instructions here: https://kubernetes.io/docs/tasks/tools/#kubectl
- `helm`: Follow instructions here: https://helm.sh/docs/intro/install/
- `openssl`: Installation method depends on your OS (https://www.openssl.org/)
- `sh`: TODO: Get rid of this dependency ([used here](https://github.com/epinio/epinio/blob/3429bb76af42ea2604631849e3834271fc917359/internal/cli/clients/client.go#L1376))

### Get yourself a cluster

You may already have a Kubernetes cluster you want to use to deploy Epinio. If
not, you can create one with [k3d](https://k3d.io/). Follow the instructions on
[the k3d.io website](https://k3d.io/) to install k3d on your system. Then get
youself a cluster with the following command:

```bash
$ k3d cluster create epinio
```

After the command returns, `kubectl` should already be talking to your new cluster:

```bash
$ kubectl get nodes
NAME                  STATUS   ROLES                  AGE   VERSION
k3d-epinio-server-0   Ready    control-plane,master   38s   v1.20.0+k3s2
```

### Install Epinio

Get the latest version of the binary that matches your Operating System here:
https://github.com/epinio/epinio/releases

Install it on your system and make sure it is in your `PATH` (or otherwise
available in your command line).

Now install Epinio on your cluster with this command:

```bash
$ epinio install
```

That's it! If everything worked as expected you are now ready to push your first
application to your Kubernetes cluster with Epinio.

### Push an application

NOTE: If you want to know the details of the `epinio push` process, read this
page: [detailed push docs](/docs/detailed-push-process.md)

Run the following command for any supported application directory. If you just
want an application that works use the one inside the [sample-app directory](sample-app).

```bash
$ epinio push sample sample-app
```

Note that the path argument is __optional__.
If not specified the __current working directory__ will be used.
Always ensure that the chosen directory contains a supported application.

If you want to know what applications are supported in Epinio, read this page: [supported applications](/docs/supported-apps.md)

### Check that your application is working

After the application has been pushed, a unique URL is printed which you can use
to access your application. If you don't have this URL available anymore you can find it again by
running:

```bash
$ epinio app show sample
```

("Routes" is the part your are looking for)

Go ahead and open the application route in your browser!

### List all commands

To see all the applications you have deployed use the following command:

```bash
$ epinio apps list
```

### Delete an application

To delete the application you just deployed run the following command:

```bash
$ epinio delete sample
```

### Create a separate org

If you want to keep your various application separated, you can use the concept
of orgs (aka organizations). Create a new organization with this command:

```bash
$ epinio orgs create neworg
```

To start deploying application to this new organization you need to "target" it:


```bash
$ epinio target neworg
```

After this and until you target another organization, whenever you run `epinio push`
you will be deploying to this new organization.

### Uninstall

NOTE: The command below will delete all the components Epinio originally installed.
**This includes all the deployed applications.**
If after installing Epinio, you deployed other things on the same cluster
that depended on those Epinio deployed components (e.g. Traefik, Tekton etc),
then removing Epinio will remove those components and this may break your other
workloads that depended on these. Make sure you understand the implications of
uninstalling Epinio before you proceed.

If you want to completely uninstall Epinio from your kubernetes cluster, you
can do this with the command:

```bash
$ epinio uninstall
```

### Read command help

Run

```bash
$ epinio --help
```

or

```bash
$ epinio COMMAND --help
```

## Configuration

Epinio places its configuration at `$HOME/.config/epinio/config.yaml` by default.

For exceptional situations this can be overriden by either specifying

  - The global command-line option `--config-file`, or

  - The environment variable `EPINIO_CONFIG`.
