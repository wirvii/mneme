# Managed Kokoro sidecar

The launcher accepts exactly one JSON request on standard input. It never accepts
spoken text through arguments or environment variables. Release CI packages it
with a private Python runtime and the platform lockfile; mneme never invokes the
user's Python installation.

`launcher.py` defines the MLX contract for macOS Apple Silicon;
`launcher_pytorch.py` implements the same protocol on Linux and Windows CPU.
