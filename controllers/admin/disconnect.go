package admin

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/owncast/owncast/controllers"
	"github.com/owncast/owncast/core"

	"github.com/owncast/owncast/core/rtmp"
)

// DisconnectInboundConnection will force-disconnect an inbound stream.
func DisconnectInboundConnection(w http.ResponseWriter, r *http.Request) {
	if !core.GetStatus().Online {
		controllers.WriteSimpleResponse(w, false, "no inbound stream connected")
		return
	}

	rtmp.Disconnect()
	controllers.WriteSimpleResponse(w, true, "inbound stream disconnected")
}

// SelfDestruct will end the owncast process.
// Hopefully it's set up with a linux service or as an auto-restarting docker container!
func SelfDestruct(w http.ResponseWriter, r *http.Request) {
	log.Println("Self-destruct initiated by admin panel. Disconnecting and exiting process in 1 second...")
	rtmp.Disconnect()
	core.SetStreamAsDisconnected()
	go (func() {
		time.Sleep(1)
		log.Println("Byeeeeee 👋")
		os.Exit(0)
	})()

	w.WriteHeader(http.StatusOK)
}
