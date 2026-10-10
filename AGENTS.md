# fnOS packaging repository

- Shared Vue / Go / translations and Docker are maintained in ../Clash-Manager, not copied into this repository.
- Pin shared source with upstream.lock (full commit SHA + VERSION); never silently build a branch head or dirty source.
- Preserve fnOS appname, paths, lifecycle, data compatibility and icon configuration.
- fpk/manifest is the FPK version source. Compile it into the isolated shared build and package its matching CHANGELOG.
- For a new user-requested FPK build, increment the manifest patch version automatically unless the user specifies a version; do not ask for a separate reminder or confirmation.
- Run ./scripts/check.sh; build x86 / arm / all serially and run scripts/audit-fpk.py for packaging changes.
- Keep final artifacts in dist/. Archive proof does not prove real-device installation or TUN behavior.
- A user request to package authorizes the necessary local commits of task changes in either repository, updating upstream.lock, and building the FPKs; do not request separate authorization for these steps. Preserve unrelated changes.
- Git push, FPK publication and Docker publication still require an explicit user request.
