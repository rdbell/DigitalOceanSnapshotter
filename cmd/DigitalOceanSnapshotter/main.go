package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/digitalocean/godo"
	log "github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

const createdAtFormat = "2006-01-02T15:04:05Z"

type snapshotterContext struct {
	DoContext    *DigitalOceanContext
	SlackContext *SlackContext
}

func initLogging() {
	log.SetFormatter(&log.TextFormatter{
		DisableLevelTruncation: true,
	})

	log.SetOutput(os.Stdout)

	log.SetLevel(log.InfoLevel)
}

func main() {
	initLogging()

	DOToken, present := os.LookupEnv("DO_TOKEN")

	if !present {
		log.Fatal("Missing enviroment variable \"DO_TOKEN\"")
	}

	volumesEnv, present := os.LookupEnv("DO_VOLUMES")

	if !present {
		log.Fatal("Missing enviroment variable \"DO_VOLUMES\"")
	}

	snapshotCountEnv, present := os.LookupEnv("DO_SNAPSHOT_COUNT")

	if !present {
		log.Fatal("Missing enviroment variable \"DO_SNAPSHOT_COUNT\"")
	}

	snapshotCount, err := strconv.Atoi(snapshotCountEnv)

	if err != nil {
		log.Fatal("Enviroment variable \"DO_SNAPSHOT_COUNT\" is not an integer")
	}

	slackEnv := os.Getenv("SLACK_TOKEN")

	var slackContext *SlackContext = nil

	if slackEnv != "" {
		channelID, present := os.LookupEnv("SLACK_CHANNEL_ID")

		if !present {
			log.Fatal("Missing enviroment variable \"SLACK_CHANNEL_ID\"")
		}

		slackContext = &SlackContext{
			client:    slack.New(slackEnv),
			channelID: channelID,
		}
	}

	ctx := snapshotterContext{
		DoContext: &DigitalOceanContext{
			client: godo.NewFromToken(DOToken),
			ctx:    context.TODO(),
		},
		SlackContext: slackContext,
	}

	volumeIDs := strings.Split(volumesEnv, ",")

	for _, volumeID := range volumeIDs {
		volume, _, err := ctx.DoContext.GetVolume(volumeID)
		if err != nil {
			handleError(ctx, err, true)
		}

		snapshot, _, err := ctx.DoContext.CreateSnapshot(&godo.SnapshotCreateRequest{
			VolumeID: volume.ID,
			Name:     time.Now().Format("2006-01-02T15:04:05"),
		})
		if err != nil {
			handleError(ctx, err, true)
		}

		log.Infof("Created Snapshot with Id %s from volume %s", snapshot.ID, volume.Name)

		snapshots, _, err := ctx.DoContext.ListSnapshots(volume.ID, &godo.ListOptions{
			PerPage: 100,
		})
		if err != nil {
			handleError(ctx, err, true)
		}

		snapshotLength := len(snapshots)

		if snapshotLength > snapshotCount {
			sort.SliceStable(snapshots, func(firstIndex, secondIndex int) bool {
				firstTime, err := time.Parse(createdAtFormat, snapshots[firstIndex].Created)
				if err != nil {
					handleError(ctx, err, true)
				}

				secondTime, err := time.Parse(createdAtFormat, snapshots[secondIndex].Created)
				if err != nil {
					handleError(ctx, err, true)
				}

				return firstTime.Before(secondTime)
			})

			snapshotsToDelete := snapshotLength - snapshotCount

			for i := 0; i < snapshotsToDelete; i++ {
				snapshotToDeleteID := snapshots[i].ID
				_, err := ctx.DoContext.DeleteSnapshot(snapshotToDeleteID)
				if err != nil {
					handleError(ctx, err, false)
					return
				}

				log.Infof("Deleted Snapshot with Id %s", snapshotToDeleteID)
			}
		}
	}

	if ctx.SlackContext != nil {
		err = ctx.SlackContext.SendEvent(fmt.Sprintf("Successfully created Backup for %d Volumes", len(volumeIDs)), log.InfoLevel)
		if err != nil {
			handleError(ctx, err, false)
		}
	}

	if successEnv, present := os.LookupEnv("SUCCESS_COMMAND"); present {
		err = exec.Command("bash", "-c", successEnv).Run()
		if err != nil {
			handleError(ctx, err, true)
		}
	}
}

func handleError(ctx snapshotterContext, err error, fatal bool) {
	errString := err.Error()

	if ctx.SlackContext != nil {
		err = ctx.SlackContext.SendEvent(errString, log.ErrorLevel)
		if err != nil {
			log.Errorf("Error while trying to send error to Slack: %s", err.Error())
		}
	}

	if fatal {
		log.Fatal(errString)
	}

	log.Error(errString)
}
