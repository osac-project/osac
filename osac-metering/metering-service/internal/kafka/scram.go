/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package kafka

import "github.com/xdg-go/scram"

// scramClient implements sarama.SCRAMClient for SCRAM-SHA-512 authentication.
type scramClient struct {
	conversation *scram.ClientConversation
}

func (c *scramClient) Begin(userName, password, authzID string) error {
	client, err := scram.SHA512.NewClient(userName, password, authzID)
	if err != nil {
		return err
	}
	c.conversation = client.NewConversation()
	return nil
}

func (c *scramClient) Step(challenge string) (string, error) {
	return c.conversation.Step(challenge)
}

func (c *scramClient) Done() bool {
	return c.conversation.Done()
}
