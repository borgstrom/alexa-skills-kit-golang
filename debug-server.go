package alexa

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/aws/aws-lambda-go/lambda"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// Run will start the associated Alexa instance to either handle requests from the AWS
// Lambda service, or if run from the ASK CLI (via `ask run`) it will run in debug mode
// against the development stage of the skill so that you can interact with your local
// code from the live Alexa service
func (alexa *Alexa) Run() {
	var (
		debugServer                  bool
		accessToken, skillId, region string
	)
	flag.BoolVar(&debugServer, "debugServer", false, "Start an alexa debug server")
	flag.StringVar(&accessToken, "accessToken", "", "The Alexa Developer lwa access token")
	flag.StringVar(&skillId, "skillId", "", "The skill ID")
	flag.StringVar(&region, "region", "NA", "The Alexa run region")
	flag.Parse()

	if debugServer {
		debug(accessToken, alexa.ApplicationID, region, alexa.ProcessRequest)
	} else {
		lambda.Start(alexa.ProcessRequest)
	}
}

type handlerFunc func(ctx context.Context, requestEnv *RequestEnvelope) (*ResponseEnvelope, error)

var regionEndpoints = map[string]string{
	"NA": "bob-dispatch-prod-na.amazon.com",
	"FE": "bob-dispatch-prod-fe.amazon.com",
	"EU": "bob-dispatch-prod-eu.amazon.com",
}

type skillRequest struct {
	Version        string `json:"version"`
	Type           string `json:"type"`
	RequestID      string `json:"requestId"`
	RequestPayload string `json:"requestPayload"`
}

type skillResponseType string

const (
	skillResponseTypeSuccess skillResponseType = "SkillResponseSuccessMessage"
	skillResponseTypeFailure skillResponseType = "SkillResponseFailureMessage"
)

type skillResponse struct {
	Version           string            `json:"version"`
	Type              skillResponseType `json:"type"`
	OriginalRequestID string            `json:"originalRequestId"`
	ResponsePayload   string            `json:"responsePayload"`
}

func debug(accessToken, skillId, region string, handler handlerFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Print("Starting go alexa debug connection")

	debugEndpointURL := fmt.Sprintf(
		"wss://%s/v1/skills/%s/stages/development/connectCustomDebugEndpoint",
		regionEndpoints[region],
		skillId,
	)

	log.Print("Connecting to: ", debugEndpointURL)

	headers := http.Header{}
	// This MUST be set directly via the map instead of using the Header methods because
	// the Alexa API hosts (i.e. bob-dispatch-prod-na.amazon.com) require it in lower case
	// ಠ_ಠ
	headers["authorization"] = []string{accessToken}

	c, _, err := websocket.Dial(ctx, debugEndpointURL, &websocket.DialOptions{
		CompressionMode: websocket.CompressionDisabled,
		HTTPHeader:      headers,
	})
	if err != nil {
		log.Fatal("Failed to connect to debug endpoint:", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "bye")

	log.Print("Debug session successfully started")
	log.Print("This session is authorized for 1 hour")

	for {
		var (
			req  *skillRequest
			resp *skillResponse
		)
		err = wsjson.Read(ctx, c, &req)
		if err != nil {
			log.Fatal("Failed to read message: ", err)
		}

		log.Print("Received message: ", req)

		var reqEnv *RequestEnvelope
		err = json.Unmarshal([]byte(req.RequestPayload), &reqEnv)
		if err != nil {
			log.Fatal("Failed to unmarshal request payload: ", err)
		}

		resp = &skillResponse{
			Type:              skillResponseTypeSuccess,
			Version:           req.Version,
			OriginalRequestID: req.RequestID,
		}

		r, err := handler(ctx, reqEnv)
		if err != nil {
			log.Print("Failed to handle skill request: ", err)
			resp.Type = skillResponseTypeFailure
		} else {
			rb, err := json.Marshal(r)
			if err != nil {
				log.Print("Failed to marshal skill response: ", err)
				resp.Type = skillResponseTypeFailure
			} else {
				resp.ResponsePayload = string(rb)
			}
		}

		log.Print("Sending response: ", resp)
		err = wsjson.Write(ctx, c, resp)
		if err != nil {
			log.Fatal("Failed to write response: ", err)
		}
	}
}
