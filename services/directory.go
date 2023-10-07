package services

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"time"

	soroban "code.samourai.io/wallet/samourai-soroban"
	"code.samourai.io/wallet/samourai-soroban/confidential"
	"code.samourai.io/wallet/samourai-soroban/internal"

	log "github.com/sirupsen/logrus"
)

// DirectoryEntries for json-rpc request
type DirectoryEntries struct {
	Name      string
	Limit     int
	PublicKey string
	Algorithm string
	Signature string
	Timestamp int64
}

// DirectoryEntriesResponse for json-rpc response
type DirectoryEntriesResponse struct {
	Name    string
	Entries []string
}

// DirectoryEntry for json-rpc request
type DirectoryEntry struct {
	Name      string
	Entry     string
	Mode      string
	PublicKey string
	Algorithm string
	Signature string
	Timestamp int64
}

// Directory struct for json-rpc
type Directory struct{}

func StartP2PDirectory(ctx context.Context, p2pSeed, bootstrap string, listenPort int, room string, ready chan struct{}) {
	if len(bootstrap) == 0 {
		log.Error("Invalid bootstrap")
		return
	}
	if len(room) == 0 {
		log.Error("Invalid room")
		return
	}

	directory := internal.DirectoryFromContext(ctx)
	if directory == nil {
		log.Error("Directory not found")
		return
	}
	p2P := internal.P2PFromContext(ctx)
	if p2P == nil {
		log.Error("p2p - P2P not found")
		return
	}

	p2pReady := make(chan struct{})
	go func() {
		err := p2P.Start(ctx, p2pSeed, listenPort, bootstrap, room, p2pReady)
		if err != nil {
			log.WithError(err).Error("Failed to p2P.Start")
		}
		ready <- struct{}{}
	}()

	<-p2pReady

	timeoutDelay := 15 * time.Minute // first timeout is longer at startup
	lastHeartbeatTimestamp := time.Now().UTC()
	for {
		select {
		case message := <-p2P.OnMessage:
			var args DirectoryEntry

			err := message.ParsePayload(&args)
			if err != nil {
				log.WithError(err).Error("Failed to ParsePayload")
				continue
			}

			if args.Name == "p2p.heartbeat" {
				timeoutDelay = 3 * time.Minute // reduce timeout delay after first heartbeat received
				lastHeartbeatTimestamp = time.Now()

				log.Debug("p2p - heartbeat received")
				continue
			}

			switch message.Context {
			case "Directory.Add":
				err = addToDirectory(directory, &args)

			case "Directory.Remove":
				err = removeFromDirectory(directory, &args)
			}
			if err != nil {
				log.WithError(err).Error("failed to process message.")
				continue
			}

		case <-time.After(30 * time.Second):
			if time.Since(lastHeartbeatTimestamp) > timeoutDelay {
				log.Warning("No message received from too long, exiting...")
				soroban.Shutdown(ctx)
				os.Exit(0)
			}

			err := p2P.PublishJson(ctx, "Directory.Add", DirectoryEntry{
				Name:  "p2p.heartbeat",
				Entry: fmt.Sprintf("%d", time.Now().Unix()),
				Mode:  "short",
			})
			if err != nil {
				// non fatal error
				log.Warningf("p2p - Failed to PublishJson. %s\n", err)
				continue
			}
			log.Debug("p2p - heartbeat sent")

		case <-ctx.Done():
			return
		}
	}
}

func (t *Directory) List(r *http.Request, args *DirectoryEntries, result *DirectoryEntriesResponse) error {
	directory := internal.DirectoryFromContext(r.Context())
	if directory == nil {
		log.Error("Directory not found")
		return nil
	}

	info := confidential.GetConfidentialInfo(args.Name, args.PublicKey)
	// check signature if key is confidential, list is not allowed for anonymous
	if info.Confidential {
		err := args.VerifySignature(info)
		if err != nil {
			log.WithError(err).Error("Failed to verifySignature")
			return nil
		}
	}

	entries, err := directory.List(args.Name)
	if err != nil {
		log.WithError(err).Error("Failed to list directory")
		return nil
	}

	if args.Limit > 0 && args.Limit < len(entries) {
		rand.Shuffle(len(entries), func(i, j int) {
			entries[i], entries[j] = entries[j], entries[i]
		})
		entries = entries[:args.Limit]
	}

	log.Tracef("List: %s (%d)", args.Name, len(entries))

	if entries == nil {
		entries = make([]string, 0)
	}
	*result = DirectoryEntriesResponse{
		Name:    args.Name,
		Entries: entries,
	}
	return nil
}

func addToDirectory(directory soroban.Directory, args *DirectoryEntry) error {
	if args == nil {
		return errors.New("invalid args")
	}
	return directory.Add(args.Name, args.Entry, directory.TimeToLive(args.Mode))
}

func (t *Directory) Add(r *http.Request, args *DirectoryEntry, result *Response) error {
	ctx := r.Context()
	directory := internal.DirectoryFromContext(ctx)
	if directory == nil {
		log.Error("Directory not found")
		return nil
	}

	p2p := internal.P2PFromContext(ctx)
	if p2p == nil {
		log.Println("p2p - P2P not found")
		return nil
	}

	info := confidential.GetConfidentialInfo(args.Name, args.PublicKey)
	// check signature if key is readonly, add is not allowed for anonymous
	if info.ReadOnly {
		err := args.VerifySignature(info)
		if err != nil {
			log.WithError(err).Error("Failed to verifySignature")
			*result = Response{
				Status: "error",
			}
			return nil
		}
	}

	log.Debugf("Add: %s %s", args.Name, args.Entry)

	err := addToDirectory(directory, args)
	if err != nil {
		log.WithError(err).Error("Failed to Add entry")
		*result = Response{
			Status: "error",
		}
		return nil
	}

	err = p2p.PublishJson(ctx, "Directory.Add", args)
	if err != nil {
		// non fatal error
		log.Printf("p2p - Failed to PublishJson. %s\n", err)
	}

	*result = Response{
		Status: "success",
	}
	return nil

}

func removeFromDirectory(directory soroban.Directory, args *DirectoryEntry) error {
	if args == nil {
		return errors.New("invalid args")
	}
	return directory.Remove(args.Name, args.Entry)
}

func (t *Directory) Remove(r *http.Request, args *DirectoryEntry, result *Response) error {
	ctx := r.Context()
	directory := internal.DirectoryFromContext(ctx)
	if directory == nil {
		log.Error("Directory not found")
		return nil
	}

	info := confidential.GetConfidentialInfo(args.Name, args.PublicKey)
	// check signature if key is readonly, remove is not allowed for anonymous
	if info.ReadOnly {
		err := args.VerifySignature(info)
		if err != nil {
			log.WithError(err).Error("Failed to verifySignature")
			return nil
		}
	}

	p2p := internal.P2PFromContext(ctx)
	if p2p == nil {
		log.Println("p2p - P2P not found")
		return nil
	}

	log.Debugf("Remove: %s %s", args.Name, args.Entry)

	status := "success"
	err := removeFromDirectory(directory, args)
	if err != nil {
		status = "error"
		log.WithError(err).Error("Failed to Remove directory")
	}

	err = p2p.PublishJson(ctx, "Directory.Remove", args)
	if err != nil {
		// non fatal error
		log.Printf("p2p - Failed to PublishJson. %s\n", err)
	}

	*result = Response{
		Status: status,
	}
	return nil
}

func timeInRange(start, end, check time.Time) bool {
	return check.After(start) && check.Before(end)
}

func (p *DirectoryEntries) VerifySignature(info confidential.ConfidentialEntry) error {
	if len(info.Prefix) == 0 || len(info.Algorithm) == 0 || len(info.PublicKey) == 0 {
		return nil
	}

	now := time.Now().UTC()
	timestamp := time.Unix(0, p.Timestamp).UTC()
	log.WithField("Timestamp", timestamp).Warning("VerifySignature")
	delta := 24 * time.Hour

	if p.PublicKey != info.PublicKey {
		return errors.New("PublicKey not allowed")
	}

	if !timeInRange(now.Add(-delta), now.Add(delta), timestamp) {
		return errors.New("timestamp not in time range")
	}

	message := fmt.Sprintf("%v.%v", p.Name, p.Timestamp/1000000)
	return confidential.VerifySignature(info, p.PublicKey, message, p.Algorithm, p.Signature)
}

func (p *DirectoryEntry) VerifySignature(info confidential.ConfidentialEntry) error {
	if len(info.Prefix) == 0 || len(info.Algorithm) == 0 || len(info.PublicKey) == 0 {
		return nil
	}

	if p.PublicKey != info.PublicKey {
		return errors.New("PublicKey not allowed")
	}

	now := time.Now().UTC()
	timestamp := time.Unix(0, p.Timestamp).UTC()
	delta := 24 * time.Hour
	if !timeInRange(now.Add(-delta), now.Add(delta), timestamp) {
		return errors.New("timestamp not in time range")
	}
	message := fmt.Sprintf("%s.%d.%s", p.Name, p.Timestamp, p.Entry)
	return confidential.VerifySignature(info, p.PublicKey, message, p.Algorithm, p.Signature)
}
