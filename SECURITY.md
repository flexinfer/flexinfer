# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| 0.x.x   | ✅ Current development |

## Reporting a Vulnerability

If you believe you have found a security vulnerability in FlexInfer, **do not open a public issue**.

### How to Report

1. Email `security@flexinfer.ai` using the PGP key below.
2. Include:
   - Description of the vulnerability
   - Steps to reproduce
   - Potential impact assessment
   - Any suggested fixes

### PGP Fingerprint

```
F1E9 3CE2 9C1B A68E 9FDB  6AC3 A9B7 0D52 4A4F 1CCE
```

### Response Timeline

- **Acknowledgment**: Within 48 hours
- **Initial assessment**: Within 5 business days
- **Fix development**: Based on severity (see below)
- **Public disclosure**: Coordinated with reporter, typically 90 days

### Severity Levels

| Severity | Response Time | Examples |
|----------|---------------|---------|
| Critical | 24 hours | Remote code execution, credential exposure |
| High | 3 business days | Privilege escalation, data exfiltration |
| Medium | 10 business days | Denial of service, information disclosure |
| Low | 30 business days | Minor information leak, hardening issue |

### Embargo Policy

- Security fixes are developed in a private branch
- Patches are released simultaneously with the advisory
- CVEs are requested for vulnerabilities with CVSS >= 4.0
- Reporters are credited unless they request anonymity

## Security Practices

### Supply Chain

- All dependencies are pinned to specific versions
- Container images are scanned with Trivy on every build
- SBOMs are generated in SPDX format for every release
- `govulncheck` runs on every CI pipeline

### Runtime Security

- Pods run with `AutomountServiceAccountToken: false` by default
- GPU workload pods do not have access to the Kubernetes API
- Network policies isolate inference pods
- Secrets are never logged or exposed in status fields

### Code Security

- `go vet` and `govulncheck` run on every CI pipeline
- Race detector (`go test -race`) is part of the test suite
- Input validation follows allowlist patterns
- No user-supplied data is used in shell commands
