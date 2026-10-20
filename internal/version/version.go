package version

// Version is the Termian build version. It is a variable so release builds can
// override it with -ldflags "-X .../internal/version.Version=v1.0.0".
var Version = "0.1.0-dev"
