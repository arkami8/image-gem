package main

import (
	"github.com/arkami8/image-gem/config"

	"github.com/davidbyttow/govips/v2/vips"
)

func main() {
	config.ReadConfig()

	vips.LoggingSettings(nil, vips.LogLevelWarning)
	vips.Startup(nil)
	defer vips.Shutdown()

	Serve()
}
