// Command winres writes rsrc_windows_amd64.syso so the Windows exe gets an icon and version info.
//
//	go run ./tools/winres
package main

import (
	"log"
	"os"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"

	"github.com/codypowpow-afk/piano-player/internal/appicon"
)

func main() {
	icon, err := winres.NewIconFromResizedImage(appicon.Image(256), []int{256, 64, 48, 32, 16})
	if err != nil {
		log.Fatal(err)
	}
	rs := &winres.ResourceSet{}
	if err := rs.SetIcon(winres.Name("APPICON"), icon); err != nil {
		log.Fatal(err)
	}
	v := version.Info{}
	v.SetFileVersion("1.0.0.0")
	v.SetProductVersion("1.0.0.0")
	v.Set(0, version.ProductName, "Piano Player")
	v.Set(0, version.FileDescription, "Piano Player - acoustic grand and upright piano")
	v.Set(0, version.OriginalFilename, "PianoPlayer.exe")
	rs.SetVersionInfo(v)
	f, err := os.Create("rsrc_windows_amd64.syso")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := rs.WriteObject(f, winres.ArchAMD64); err != nil {
		log.Fatal(err)
	}
}
