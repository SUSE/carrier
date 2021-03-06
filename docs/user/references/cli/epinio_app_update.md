---
title: "epinio app update"
linkTitle: "epinio app update"
weight: 1
---
## epinio app update

Update the named application

### Synopsis

Update the running application's attributes (e.g. instances)

```
epinio app update NAME [flags]
```

### Options

```
  -h, --help              help for update
  -i, --instances int32   The number of instances the application should have (default 1)
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

* [epinio app](../epinio_app)	 - Epinio application features

