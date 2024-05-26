package main

import (
	"strings"

	"github.com/mattermost/mattermost-server/v5/model"
	"github.com/mattermost/mattermost-server/v5/plugin"
)

func (p *Plugin) OnActivate() error {
	// Register a command to create the AI assistant user
	p.API.RegisterCommand(&model.Command{
		Trigger:          "createai",
		DisplayName:      "Create AI Assistant",
		Description:      "Create a synthetic user that acts as an AI assistant.",
		AutoComplete:     true,
		AutoCompleteDesc: "Create an AI assistant user.",
		AutoCompleteHint: "[username]",
	})
	return nil
}

func (p *Plugin) ExecuteCommand(c *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	if args.Command == "/createai" {
		parts := strings.Fields(args.Command)
		if len(parts) != 2 {
			return &model.CommandResponse{
				Text: "Usage: /createai [username]",
			}, nil
		}

		username := parts[1]

		user := &model.User{
			Username: username,
			Password: "securepassword", // Generate a secure password in production
			Email:    username + "@example.com",
			Nickname: "AI Assistant",
			Roles:    "system_user",
		}

		if _, err := p.API.CreateUser(user); err != nil {
			return &model.CommandResponse{
				Text: "Failed to create AI assistant user: " + err.Error(),
			}, nil
		}

		return &model.CommandResponse{
			Text: "AI assistant user created successfully.",
		}, nil
	}

	return &model.CommandResponse{}, nil
}

// Register the event handler
func (p *Plugin) MessageHasBeenPosted(c *plugin.Context, post *model.Post) {
	if post.UserId == p.API.GetConfig().BotSettings.BotUserID {
		return
	}

	aiUser, err := p.API.GetUserByUsername("ai-assistant") // Adjust the username as needed
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
