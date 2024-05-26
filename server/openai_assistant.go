package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/mattermost/mattermost-server/v5/plugin"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

type Configuration struct {
	OpenAIAPIKey string
	AssistantID  string
}

type Plugin struct {
	plugin.MattermostPlugin
	configuration *Configuration
}

type Message struct {
	Role     string `json:"role"`
	Content  string `json:"content"`
	ThreadID string `json:"threadID"`
}

func (p *Plugin) OnActivate() error {
	// Register a command to create the AI assistant user
	p.API.RegisterCommand(&model.Command{
		Trigger: "createai",
		// Add command handling logic here
	})
	return nil
}

func (p *Plugin) ServeHTTP(c *plugin.Context, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	var messages []Message
	err := json.NewDecoder(r.Body).Decode(&messages)
	if err != nil {
		http.Error(w, "Failed to decode request body", http.StatusBadRequest)
		return
	}

	threadID, err := p.getOrCreateThreadID(messages)
	if err != nil {
		http.Error(w, "Failed to get or create thread ID", http.StatusInternalServerError)
		return
	}

	response, err := p.sendMessageToAssistant(threadID, messages)
	if err != nil {
		http.Error(w, "Failed to send message to assistant", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (p *Plugin) getOrCreateThreadID(messages []Message) (string, error) {
	if messages[0].ThreadID == "0" {
		threadResponse, err := p.createThread()
		if err != nil {
			return "", err
		}
		return threadResponse.ID, nil
	}
	return messages[0].ThreadID, nil
}

func (p *Plugin) createThread() (*ThreadResponse, error) {
	client := resty.New()
	resp, err := client.R().
		SetHeader("Authorization", "Bearer "+p.configuration.OpenAIAPIKey).
		SetHeader("Content-Type", "application/json").
		Post("https://api.openai.com/v2/threads")

	if err != nil {
		return nil, err
	}

	var threadResponse ThreadResponse
	err = json.Unmarshal(resp.Body(), &threadResponse)
	if err != nil {
		return nil, err
	}

	return &threadResponse, nil
}

func (p *Plugin) sendMessageToAssistant(threadID string, messages []Message) (*AssistantResponse, error) {
	client := resty.New()
	for _, message := range messages {
		_, err := client.R().
			SetHeader("Authorization", "Bearer "+p.configuration.OpenAIAPIKey).
			SetHeader("Content-Type", "application/json").
			SetBody(message).
			Post("https://api.openai.com/v2/threads/" + threadID + "/messages")

		if err != nil {
			return nil, err
		}
	}

	runResponse, err := p.runAssistant(threadID)
	if err != nil {
		return nil, err
	}

	return p.pollForCompletion(threadID, runResponse.ID)
}

func (p *Plugin) runAssistant(threadID string) (*RunResponse, error) {
	client := resty.New()
	resp, err := client.R().
		SetHeader("Authorization", "Bearer "+p.configuration.OpenAIAPIKey).
		SetHeader("Content-Type", "application/json").
		SetBody(map[string]string{"assistant_id": p.configuration.AssistantID}).
		Post("https://api.openai.com/v2/threads/" + threadID + "/runs")

	if err != nil {
		return nil, err
	}

	var runResponse RunResponse
	err = json.Unmarshal(resp.Body(), &runResponse)
	if err != nil {
		return nil, err
	}

	return &runResponse, nil
}

func (p *Plugin) pollForCompletion(threadID, runID string) (*AssistantResponse, error) {
	client := resty.New()
	startTime := time.Now()
	for {
		resp, err := client.R().
			SetHeader("Authorization", "Bearer "+p.configuration.OpenAIAPIKey).
			Get("https://api.openai.com/v2/threads/" + threadID + "/runs/" + runID)

		if err != nil {
			return nil, err
		}

		var runStatus RunStatus
		err = json.Unmarshal(resp.Body(), &runStatus)
		if err != nil {
			return nil, err
		}

		if runStatus.Status == "completed" {
			return p.getMessages(threadID)
		}

		if time.Since(startTime) > 29*time.Second {
			return nil, errors.New("timeout waiting for the run to complete")
		}

		time.Sleep(500 * time.Millisecond)
	}
}

func (p *Plugin) getMessages(threadID string) (*AssistantResponse, error) {
	client := resty.New()
	resp, err := client.R().
		SetHeader("Authorization", "Bearer "+p.configuration.OpenAIAPIKey).
		Get("https://api.openai.com/v2/threads/" + threadID + "/messages")

	if err != nil {
		return nil, err
	}

	var messagesResponse MessagesResponse
	err = json.Unmarshal(resp.Body(), &messagesResponse)
	if err != nil {
		return nil, err
	}

	for _, message := range messagesResponse.Data {
		if message.Role == "assistant" {
			for _, content := range message.Content {
				if content.Type == "text" {
					return &AssistantResponse{
						Status:   "success",
						ThreadID: threadID,
						Message:  content.Text.Value,
					}, nil
				}
			}
		}
	}

	return nil, errors.New("assistant's final response not found")
}
