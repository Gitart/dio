package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	rq "github.com/parnurzeal/gorequest"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var pullCmdBranch, pullCmdCommit string

// Downloads a database from DBHub.io.
var pullCmd = &cobra.Command{
	Use:   "pull [database name]",
	Short: "Download a database from DBHub.io",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Ensure a database file was given
		if len(args) == 0 {
			return errors.New("No database file specified")
		}
		// TODO: Allow giving multiple database files on the command line.  Hopefully just needs turning this
		// TODO  into a for loop
		if len(args) > 1 {
			return errors.New("Only one database can be downloaded at a time (for now)")
		}

		// TODO: Add a --licence option, for automatically grabbing the licence as well
		//       * Probably save it as <database name>-<license short name>.txt/html

		// Ensure we weren't given potentially conflicting info on what to pull down
		if pullCmdBranch != "" && pullCmdCommit != "" {
			return errors.New("Either a branch name or commit ID can be given.  Not both at the same time!")
		}

		// Retrieve metadata for the database
		var meta metaData
		var err error
		db := args[0]
		meta, err = updateMetadata(db, false) // Don't store the metadata to disk yet, in case the download fails
		if err != nil {
			return err
		}

		// If given, make sure the requested branch or commit exist
		if pullCmdBranch != "" {
			if _, ok := meta.Branches[pullCmdBranch]; ok == false {
				return errors.New("The requested branch doesn't exist")
			}
		}
		if pullCmdCommit != "" {
			if _, ok := meta.Commits[pullCmdCommit]; ok == false {
				return errors.New("The requested commit doesn't exist")
			}
		}

		// Download the database file
		dbURL := fmt.Sprintf("%s/%s/%s", cloud, certUser, db)
		req := rq.New().TLSClientConfig(&TLSConfig).Get(dbURL)
		if pullCmdBranch != "" {
			req.Query(fmt.Sprintf("branch=%s", url.QueryEscape(pullCmdBranch)))
		} else {
			req.Query(fmt.Sprintf("commit=%s", url.QueryEscape(pullCmdCommit)))
		}
		resp, body, errs := req.End()
		if errs != nil {
			log.Print("Errors when downloading database:")
			for _, err := range errs {
				log.Print(err.Error())
			}
			return errors.New("Error when downloading database")
		}
		if resp.StatusCode != http.StatusOK {
			if resp.StatusCode == http.StatusNotFound {
				if pullCmdBranch != "" {
					return errors.New("That database & branch aren't known on DBHub.io")
				}
				if pullCmdCommit != "" {
					return errors.New(fmt.Sprintf("Requested database not found with commit %s.",
						pullCmdCommit))
				}
				return errors.New("Requested database not found")
			}
			return errors.New(fmt.Sprintf("Download failed with an error: HTTP status %d - '%v'\n",
				resp.StatusCode, resp.Status))
		}

		// Create the local database cache directory, if it doesn't yet exist
		if _, err = os.Stat(filepath.Join(".dio", db, "db")); os.IsNotExist(err) {
			err = os.MkdirAll(filepath.Join(".dio", db, "db"), 0770)
			if err != nil {
				return err
			}
		}

		// Calculate the sha256 of the database file
		s := sha256.Sum256([]byte(body))
		shaSum := hex.EncodeToString(s[:])

		// Write the database file to disk in the cache directory
		err = ioutil.WriteFile(filepath.Join(".dio", db, "db", shaSum), []byte(body), 0644)
		if err != nil {
			return err
		}

		// Write the database file to disk again, this time in the working directory
		err = ioutil.WriteFile(db, []byte(body), 0644)
		if err != nil {
			return err
		}

		// If the headers included the modification-date parameter for the database, set the last accessed and last
		// modified times on the new database file
		if disp := resp.Header.Get("Content-Disposition"); disp != "" {
			s := strings.Split(disp, ";")
			if len(s) == 4 {
				a := strings.TrimLeft(s[2], " ")
				if strings.HasPrefix(a, "modification-date=") {
					b := strings.Split(a, "=")
					c := strings.Trim(b[1], "\"")
					lastMod, err := time.Parse(time.RFC3339, c)
					if err != nil {
						return err
					}
					err = os.Chtimes(db, time.Now(), lastMod)
					if err != nil {
						return err
					}
				}
			}
		}

		// If the server provided a branch name, add it to the local metadata cache
		if branch := resp.Header.Get("Branch"); branch != "" {
			meta.ActiveBranch = branch
		}

		// The download succeeded, so save the updated metadata to disk
		err = saveMetadata(db, meta)
		if err != nil {
			return err
		}

		if pullCmdBranch != "" {
			_, err = numFormat.Printf("Database '%s' downloaded from %s.  Size: %d bytes\nBranch: '%s'\n", db,
				cloud, len(body), pullCmdBranch)
			if err != nil {
				return err
			}
			return nil
		}
		if comID := resp.Header.Get("Commit-Id"); comID != "" {
			_, err = numFormat.Printf("Database '%s' downloaded from %s.  Size: %d bytes\nCommit: %s\n", db,
				cloud, len(body), comID)
			if err != nil {
				return err
			}
			return nil
		}

		// Generic success message, when branch and commit id aren't known
		_, err = numFormat.Printf("Database '%s' downloaded.  Size: %d bytes\n", db, len(body))
		if err != nil {
			return err
		}
		return nil
	},
}

func init() {
	RootCmd.AddCommand(pullCmd)
	pullCmd.Flags().StringVar(&pullCmdBranch, "branch", "",
		"Remote branch the database will be downloaded from")
	pullCmd.Flags().StringVar(&pullCmdCommit, "commit", "", "Commit ID of the database to download")
}
