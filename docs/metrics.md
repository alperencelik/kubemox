# Metrics

Kubemox provides a set of metrics to monitor itself and Proxmox resources it manages. The metrics are exposed via HTTP endpoint `/metrics` on port `http` by default. Metrics has been provided by kubebuilder and also by kubemox itself. The general metrics provided by kubebuilder are mostly about the controller manager. The metrics provided by kubemox are about the Proxmox resources it manages and all custom metrics has been prefixed with `kubemox_` . The metrics are compatible with Prometheus. You can use Prometheus to scrape the metrics and visualize them with Grafana. Here is an example of `ServiceMonitor` for scraping the metrics.

```yaml
cat <<EOF | kubectl apply -f -
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: kubemox-monitor
  labels:
    prometheus: kube-prom-stack
    release: kube-prom-stack
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: kubemox
  endpoints:
  - port: http
    path: /metrics
    interval: 30s
  namespaceSelector:
    matchNames:
    - default
EOF
```

For visualization, you can use Grafana. The example can be found in `grafana/custom-metrics/custom-metrics-dashboard.json`. You can import that dashboard to your Grafana instance.