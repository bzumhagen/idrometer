# Security policy

Please report security problems privately through GitHub's "Report a vulnerability" (Security tab) rather than a public issue.

Things to know when deploying: the iSpindel can only send plain HTTP, so keep the ingest endpoint on a trusted network;
the web UI is readable by anyone who can reach it, and editing requires the admin password. See `docs/install.md`.
