package directhls

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hirochachacha/go-smb2"
	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/config"
	"github.com/owncast/owncast/core/data"
	"github.com/owncast/owncast/models"
)

var _sambaHostPort string
var _sambaFolderPath string
var _sambaShareName string
var _streamSegmentRegex *regexp.Regexp
var _streamStartedTime string

var _setStreamAsConnected func()
var _setStreamAsDisconnected func()
var _setBroadcaster func(models.Broadcaster)

// Start starts the directhls service, polling for HLS segments at the DirectHLSInputURL.
func Start(setStreamAsConnected func(), setBroadcaster func(models.Broadcaster), setStreamAsDisconnected func()) {
	_setStreamAsConnected = setStreamAsConnected
	_setStreamAsDisconnected = setStreamAsDisconnected
	_setBroadcaster = setBroadcaster
	_streamSegmentRegex = regexp.MustCompile(`(stream)[_-]?([^#\s]+)\.ts`)

	directHLSInputURLString := data.GetDirectHLSInputURL()

	directHLSInputURL, err := url.Parse(directHLSInputURLString)
	if err != nil {
		log.Panicf(
			"owncast can't stream because directHLSInputURL '%s' could not be parsed as a URL: %s",
			directHLSInputURLString, err,
		)
	}

	// TODO introduce retry logic, dont just panic when connections fail
	// TODO support file:// and http:// protocols as well.

	if strings.ToLower(directHLSInputURL.Scheme) == "smb" || strings.ToLower(directHLSInputURL.Scheme) == "samba" {
		connectPort := "445"
		if directHLSInputURL.Port() != "" {
			connectPort = directHLSInputURL.Port()
		}

		pathPartsRaw := strings.Split(directHLSInputURL.Path, "/")
		pathParts := []string{}
		for _, part := range pathPartsRaw {
			if strings.TrimSpace(part) != "" {
				pathParts = append(pathParts, part)
			}
		}

		if len(pathParts) > 0 {
			_sambaShareName = pathParts[0]
		}
		if len(pathParts) > 1 {
			_sambaFolderPath = path.Join(pathParts[1:]...)
		}

		_sambaHostPort = fmt.Sprintf("%s:%s", directHLSInputURL.Host, connectPort)

		tcpConnection, err := net.Dial("tcp", _sambaHostPort)
		if err != nil {
			log.Panicf(
				"owncast can't stream because it could not dial '%s' over TCP for direct HLS input: %s",
				_sambaHostPort, err,
			)
		}
		defer tcpConnection.Close()

		smbDialer := &smb2.Dialer{
			Initiator: &smb2.NTLMInitiator{
				User:     "guest",
				Password: "",
			},
		}

		smbConnection, err := smbDialer.Dial(tcpConnection)
		if err != nil {
			log.Panicf(
				"owncast can't stream because it couldn't initiate a samba session with '%s' for direct HLS input. (TCP connection established, but samba protocol failed): %s",
				_sambaHostPort, err,
			)
		}
		defer smbConnection.Logoff()

		if _sambaShareName == "" {
			shareNames, err := smbConnection.ListSharenames()
			if err != nil {
				log.Panicf(
					"owncast can't stream because it connected to the samba server '%s' for direct HLS input, but hit an error trying to list the samba share names. You could try putting the share name into the directHLSInputURL",
					_sambaHostPort,
				)
			}
			for _, name := range shareNames {
				if (strings.ToLower(name) == "public" && _sambaShareName == "") || strings.ToLower(name) == "owncast" {
					_sambaShareName = name
				}
			}
		}
		if _sambaShareName == "" {
			log.Panicf(
				"owncast can't stream because it didn't find a samba share named 'public' or 'owncast' on '%s' for direct HLS input",
				_sambaHostPort,
			)
		}

		sambaShare, err := smbConnection.Mount(_sambaShareName)
		if err != nil {
			var allShareNames string
			shareNames, err := smbConnection.ListSharenames()
			if err == nil {
				allShareNames = strings.Join(shareNames, ",\n")
			} else {
				allShareNames = fmt.Sprintf("<error occurred obtaining share names: %s>", err)
			}
			log.Panicf(
				"owncast can't stream because it couldn't mount the samba share named '%s' on '%s' for direct HLS input: %s. The existing shares are named: \n\n%s",
				_sambaShareName, _sambaHostPort, err, allShareNames,
			)
		}
		defer sambaShare.Umount()

		// this samba client doesnt like leading slashes in paths.
		_sambaFolderPath = strings.TrimPrefix(_sambaFolderPath, "/")

		fileInfos, err := sambaShare.ReadDir(_sambaFolderPath)
		if err != nil {
			log.Panicf(
				"owncast can't stream because it couldn't find the folder '%s' on the samba share named '%s' on '%s' for direct HLS input: %s",
				_sambaFolderPath, _sambaShareName, _sambaHostPort, err,
			)
		}

		log.Printf("mounted the folder '%s' on the samba share '%s' on server '%s' for direct HLS input", _sambaFolderPath, _sambaShareName, _sambaHostPort)

		mostRecentModTime := getMostRecentModTime(fileInfos)
		hlsSegmentServiceClient := http.Client{
			Timeout: time.Second * 5,
		}

		for {
			time.Sleep(time.Second * 2)

			fileInfos, err := sambaShare.ReadDir(_sambaFolderPath)
			if err != nil {
				log.Errorf(
					"couldn't find the folder '%s' on the samba share named '%s' on '%s' for direct HLS input: %s. Stream wont work if this error keeps happening",
					_sambaFolderPath, _sambaShareName, _sambaHostPort, err,
				)
			}
			modTimeNow := getMostRecentModTime(fileInfos)
			if modTimeNow.After(mostRecentModTime) {
				if _setStreamAsConnected != nil {
					_setStreamAsConnected()
				}

				// TODO how much of this can be pulled from the HLS video stream ?
				// TODO how much of this is necessary for the system to work?
				_setBroadcaster(models.Broadcaster{
					RemoteAddr:    _sambaHostPort,
					Time:          time.Now(),
					StreamDetails: models.InboundStreamDetails{},
				})

				streamStartedTimeBuffer := make([]byte, 8)
				binary.BigEndian.PutUint64(streamStartedTimeBuffer, uint64(time.Now().Unix()))
				truncatedStreamStartedTimeBuffer := []byte{}
				for _, bite := range streamStartedTimeBuffer {
					//log.Println(bite)
					if bite != byte(255) && bite != byte(0) {
						truncatedStreamStartedTimeBuffer = append(truncatedStreamStartedTimeBuffer, bite)
					}
				}
				_streamStartedTime = base64.RawURLEncoding.EncodeToString(truncatedStreamStartedTimeBuffer)

				log.Printf("now streaming files from '%s' on the samba share '%s' on server '%s' for direct HLS!!", _sambaFolderPath, _sambaShareName, _sambaHostPort)

				// TODO call SetStreamAsDisconnected() when the files stop getting updated for a couple seconds.

				for {
					// if it has been 10 seconds without any updates to files in the share, we should consider the stream disconnected.
					if time.Now().After(mostRecentModTime.Add(time.Second * 15)) {
						_setStreamAsDisconnected()
						break
					}

					fileInfos, err := sambaShare.ReadDir(_sambaFolderPath)
					if err != nil {
						log.Errorf(
							"couldn't find the folder '%s' on the samba share named '%s' on '%s' for direct HLS input: %s. Stream wont work if this error keeps happening",
							_sambaFolderPath, _sambaShareName, _sambaHostPort, err,
						)
					}

					waitGroup := new(sync.WaitGroup)

					for _, fileInfo := range fileInfos {
						if fileInfo.ModTime().After(mostRecentModTime) {
							waitGroup.Add(1)
							go (func(fileInfo os.FileInfo) {
								defer waitGroup.Done()

								file, err := sambaShare.Open(path.Join(_sambaFolderPath, fileInfo.Name()))
								if err != nil {
									log.Errorf(
										"samba client unable to open file '%s/%s' on share '%s' on server '%s' for direct HLS input: %s. Stream wont work if this error keeps happening",
										_sambaFolderPath, fileInfo.Name(), _sambaShareName, _sambaHostPort, err,
									)
									return
								}
								defer file.Close()

								var dataToSend io.Reader
								if strings.HasSuffix(fileInfo.Name(), ".m3u8") {
									playlistBytes, err := io.ReadAll(file)
									if err != nil {
										log.Errorf(
											"samba client unable to read file '%s/%s' on share '%s' on server '%s' for direct HLS input: %s. Stream wont work if this error keeps happening",
											_sambaFolderPath, fileInfo.Name(), _sambaShareName, _sambaHostPort, err,
										)
										return
									}

									dataToSend = bytes.NewBuffer(
										_streamSegmentRegex.ReplaceAll(playlistBytes, []byte(fmt.Sprintf("$1-%s-$2.ts", _streamStartedTime))),
									)
								} else {
									dataToSend = file
								}

								fileName := _streamSegmentRegex.ReplaceAllString(fileInfo.Name(), fmt.Sprintf("$1-%s-$2.ts", _streamStartedTime))
								// TODO I have hardcoded stream ID 0 here...
								hlsUploadRequest, err := http.NewRequest(
									"PUT",
									fmt.Sprintf("http://127.0.0.1:%s/0/%s", config.InternalHLSListenerPort, fileName),
									dataToSend,
								)
								if err != nil {
									log.Errorf(
										"unable to create local server HTTP PUT request for HLS segment '%s': %s. Stream wont work if this error keeps happening",
										fileInfo.Name(), err,
									)
									return
								}

								response, err := hlsSegmentServiceClient.Do(hlsUploadRequest)
								if err != nil {
									log.Errorf(
										"local server HTTP PUT request for HLS segment '%s' failed: %s. Stream wont work if this error keeps happening",
										fileInfo.Name(), err,
									)
									return
								}
								if response.StatusCode != http.StatusOK {
									log.Errorf(
										"local server HTTP PUT request for HLS segment '%s' returned HTTP %d: %s. Stream wont work if this error keeps happening",
										fileInfo.Name(), response.StatusCode, response.Status,
									)
								}

							})(fileInfo)
						}
					}

					timeBeforeSync := time.Now()
					waitGroup.Wait()
					syncDuration := time.Since(timeBeforeSync)

					mostRecentModTime = getMostRecentModTime(fileInfos)

					if syncDuration < time.Second {
						time.Sleep(time.Second - syncDuration)
					}
				}
			}

			time.Sleep(time.Second * 4)
		}

	} else {
		log.Errorf(
			"owncast can't stream because directHLSInputURL '%s' is using the protocol %s which isn't supported yet. Try samba:// or smb:// instead.",
			directHLSInputURLString, directHLSInputURL.Scheme,
		)
		return
	}
}

func getMostRecentModTime(fileInfos []os.FileInfo) time.Time {
	var mostRecentTime time.Time
	for _, fileInfo := range fileInfos {
		modTime := fileInfo.ModTime()
		if modTime.After(mostRecentTime) {
			mostRecentTime = modTime
		}
	}
	return mostRecentTime
}
