## Clamav Rest Service Change Log

### [Unreleased]

- Stream uploads to disk instead of buffering them in memory; uploads over `MAX_UPLOAD_SIZE_MB` now get a 413 response
- Update the ClamAV base image to 1.5.4 and the pinned curl package to `8.14.1-2+deb13u7`

### [v0.1.2] (12/05/2026)

- Send proper error responses in case a scan failed instead of an internal server error

### [v0.1.1] (Feb. 2026)

- Use `/var/lib/clamav-data` as storage location for virus definitions and initialize it with existing definitions from the base image if available.

### [v0.1.0] (Feb. 2026)

Initial release.
