# Security policy

## Supported versions

| Version | Supported |
| --- | --- |
| Latest v0.x release | Yes |
| Older pre-release builds | No |

Until v1.0, security fixes are released only from the latest development line.

## Reporting a vulnerability

Do not open a public issue or discussion.

Use GitHub's **Report a vulnerability** option in the repository Security tab to submit a private vulnerability report. Include:

- affected version and operating system;
- impact and realistic attack scenario;
- minimal reproduction or proof of concept;
- suggested mitigation, if known.

Remove real database credentials, query data and personally identifiable information.

The maintainer will acknowledge a complete report within seven days, provide status updates when material progress occurs and coordinate disclosure after a fix is available. Timelines may vary for issues involving upstream drivers or operating-system keyrings.

## Security boundaries

Charta's mutation confirmation and local SQL diagnostics are usability features, not authorization controls. Use least-privilege database accounts, database-side access controls and verified TLS settings.
