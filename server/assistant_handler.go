package main

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/mattermost/mattermost-server/v5/model"
	"github.com/mattermost/mattermost-server/v5/plugin"
)

type Message struct {
	Role     string `json:"role"`
	Content  string `json:"content"`
	ThreadID string `json:"threadID"`
}

type ThreadResponse struct {
	ID string `json:"id"`
}

type RunResponse struct {
	ID string `json:"id"`
}

type RunStatus struct {
	Status string `json:"status"`
}

type MessagesResponse struct {
	Data []Message `json:"data"`
}

type AssistantResponse struct {
	Status   string `json:"status"`
	ThreadID string `json:"threadID"`
	Message  string `json:"message"`
}

func (p *Plugin) MessageHasBeenPosted(c *plugin.Context, post *model.Post) {
	// Ignore posts by the AI assistant
	if post.UserId == p.API.GetConfig().BotSettings.BotUserID {
		return
	}

	// Fetch the channel to get the users in it
	channel, err := p.API.GetChannel(post.ChannelId)
	if err != nil {
		p.API.LogError("Failed to get channel", "error", err.Error())
		return
	}

	// Fetch the AI assistant user
	aiUser, err := p.API.GetUserByUsername("ai-assistant") // Change the username accordingly
	if err != nil {
		p.API.LogError("Failed to get AI assistant user", "error", err.Error())
		return
	}

	// Create a message to send to the OpenAI assistant
	messages := []Message{
		{
			Role:     "user",
			Content:  post.Message,
			ThreadID: "0", // Use "0" for new threads
		},
	}

	// Call the OpenAI assistant
	assistantResponse, err := p.callOpenAIAssistant(messages)
	if err != nil {
		p.API.LogError("Failed to call OpenAI assistant", "error", err.Error())
		return
	}

	// Post the AI assistant's response back to the channel
	aiPost := &model.Post{
		UserId:    aiUser.Id,
		ChannelId: post.ChannelId,
		Message:   assistantResponse.Message,
	}

	if _, err := p.API.CreatePost(aiPost); err != nil {
		p.API.LogError("Failed to create post", "error", err.Error())
	}
}

func (p *Plugin) callOpenAIAssistant(messages []Message) (*AssistantResponse, error) {
	client := resty.New()
	var threadID string
	if messages[0].ThreadID == "0" {
		threadResponse, err := p.createThread()
		if err != nil {
			return nil, err
		}
		threadID = threadResponse.ID
	} else {
		threadID = messages[0].ThreadID
	}

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
			SetHeader("Authorization", "Bearer "+p.configuration.OpenAIAssistantAPIKey).
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
