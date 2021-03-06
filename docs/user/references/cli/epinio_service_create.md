---
title: "epinio service create"
linkTitle: "epinio service create"
weight: 1
---
## epinio service create

Create a service

### Synopsis

Create service by name, class, plan, and optional json data.

```
epinio service create NAME CLASS PLAN [flags]
```

### Options

```
      --data string   json data to be passed to the underlying service as parameters
      --dont-wait     Return immediately, without waiting for the service to be provisioned
  -h, --help          help for create
```

### Options inherited from parent commands

```
      --config-file string       (EPINIO_CONFIG) set path of configuration file (default "~/.config/epinio/config.yaml")
  -c, --kubeconfig string        (KUBECONFIG) path to a kubeconfig, not required in-cluster
      --no-colors                Suppress colorized output
      --skip-ssl-verification    (SKIP_SSL_VERIFICATION) Skip the verification of TLS certificates
      --timeout-multiplier int   (EPINIO_TIMEOUT_MULTIPLIER) Multiply timeouts by this factor (default 1)
      --trace-level int          (TRACE_LEVEL) Only print trace messages at or above this level (0 to 5, default 0, print nothing)
      --verbosity int            (VERBOSITY) Only print progress messages at or above this level (0 or 1, default 0)
```

### SEE ALSO

* [epinio service](../epinio_service)	 - Epinio service features

