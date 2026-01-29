# KubeMacPool Metrics

| Name | Kind | Type | Description |
|------|------|------|-------------|
| kmp_mac_collisions | Metric | Gauge | Count of running objects sharing the same MAC address (collision when > 1) |
| kubevirt_kmp_duplicate_macs | Metric | Counter | Kubemacpool duplicate macs counter |

## Developing new metrics

All metrics documented here are auto-generated and reflect exactly what is being
exposed. After developing new metrics or changing old ones please regenerate
this document.
