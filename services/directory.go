package services

import (
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"time"

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

	log.Debugf("List: %s (%d)", args.Name, len(entries))

	if entries == nil {
		entries = make([]string, 0)
	}
	*result = DirectoryEntriesResponse{
		Name:    args.Name,
		Entries: entries,
	}
	return nil
}

func (t *Directory) Add(r *http.Request, args *DirectoryEntry, result *Response) error {
	directory := internal.DirectoryFromContext(r.Context())
	if directory == nil {
		log.Error("Directory not found")
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

	err := directory.Add(args.Name, args.Entry, directory.TimeToLive(args.Mode))
	if err != nil {
		log.WithError(err).Error("Failed to Add entry")
		*result = Response{
			Status: "error",
		}
		return nil
	}

	*result = Response{
		Status: "success",
	}
	return nil

}

func (t *Directory) Remove(r *http.Request, args *DirectoryEntry, result *Response) error {
	directory := internal.DirectoryFromContext(r.Context())
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

	log.Debugf("Remove: %s %s", args.Name, args.Entry)

	status := "success"
	err := directory.Remove(args.Name, args.Entry)
	if err != nil {
		status = "error"
		log.WithError(err).Error("Failed to Remove directory")
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
