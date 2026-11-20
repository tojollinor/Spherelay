package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type CredentialProfile struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	DiscoveryCredentials
}

func (s *Server) readCredentialProfiles() ([]CredentialProfile, error) {
	data, err := os.ReadFile(filepath.Join(s.dataDir, "discovery-credentials.json"))
	if os.IsNotExist(err) {
		return []CredentialProfile{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("Suchzugänge konnten nicht gelesen werden")
	}
	var envelope struct {
		Secret string `json:"secret"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return nil, fmt.Errorf("Gespeicherte Suchzugänge sind beschädigt")
	}
	plain, err := unprotectSecret(envelope.Secret)
	if err != nil {
		return nil, fmt.Errorf("Suchzugänge konnten auf diesem PC nicht entschlüsselt werden")
	}
	profiles := []CredentialProfile{}
	if strings.HasPrefix(strings.TrimSpace(plain), "[") {
		err = json.Unmarshal([]byte(plain), &profiles)
	} else {
		var old DiscoveryCredentials
		err = json.Unmarshal([]byte(plain), &old)
		profiles = append(profiles, CredentialProfile{ID: "legacy", Description: "Übernommener Kamerazugang", DiscoveryCredentials: old})
	}
	if err != nil || len(profiles) > 16 {
		return nil, fmt.Errorf("Ungültige Suchzugänge")
	}
	for _, profile := range profiles {
		if profile.ID == "" || profile.Username == "" || profile.Password == "" {
			return nil, fmt.Errorf("Ungültiger gespeicherter Suchzugang")
		}
	}
	return profiles, nil
}

func (s *Server) readDiscoveryCredentials() (*DiscoveryCredentials, error) {
	profiles, err := s.readCredentialProfiles()
	if err != nil || len(profiles) == 0 {
		return nil, err
	}
	copy := profiles[0].DiscoveryCredentials
	return &copy, nil
}

func (s *Server) writeCredentialProfiles(profiles []CredentialProfile) error {
	file := filepath.Join(s.dataDir, "discovery-credentials.json")
	if len(profiles) == 0 {
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("Suchzugang konnte nicht entfernt werden")
		}
		return nil
	}
	plain, _ := json.Marshal(profiles)
	encrypted, err := protectSecret(string(plain))
	if err != nil {
		return fmt.Errorf("Suchzugänge konnten nicht verschlüsselt werden")
	}
	data, _ := json.Marshal(struct {
		Secret string `json:"secret"`
	}{encrypted})
	if os.WriteFile(file+".tmp", data, 0600) != nil {
		return fmt.Errorf("Suchzugänge konnten nicht gespeichert werden")
	}
	if replaceFile(file+".tmp", file) != nil {
		return fmt.Errorf("Suchzugänge konnten nicht gespeichert werden")
	}
	return nil
}

func (s *Server) selectCredentialProfile(id string) (*CredentialProfile, error) {
	profiles, err := s.readCredentialProfiles()
	if err != nil {
		return nil, err
	}
	for _, profile := range profiles {
		if id == profile.ID || id == "" {
			copy := profile
			return &copy, nil
		}
	}
	return nil, fmt.Errorf("Gespeicherten Kamerazugang auswählen")
}

func (s *Server) discoveryCredentialsAPI(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	profiles, err := s.readCredentialProfiles()
	if err != nil {
		bad(w, 500, err.Error())
		return
	}
	if r.Method == "POST" {
		var input CredentialProfile
		if !readReq(w, r, &input) {
			return
		}
		input.Description = strings.TrimSpace(input.Description)
		input.Username = strings.TrimSpace(input.Username)
		index := -1
		if input.ID == "" && input.Description == "" && len(profiles) == 1 {
			index = 0
			input.ID = profiles[0].ID
			input.Description = profiles[0].Description
		}
		for i, p := range profiles {
			if p.ID == input.ID {
				index = i
			}
		}
		if input.ID != "" && index < 0 {
			bad(w, 404, "Suchzugang nicht gefunden")
			return
		}
		if input.Description == "" {
			input.Description = "Kamerazugang"
		}
		if len(input.Description) > 160 || input.Username == "" || len(input.Username) > 128 || strings.ContainsAny(input.Username, "\r\n:") || len(input.Password) > 1024 || strings.ContainsAny(input.Password, "\r\n") {
			bad(w, 400, "Gültige Beschreibung, Kamerabenutzer und Passwort eingeben")
			return
		}
		if input.Password == "" && index >= 0 && input.Username == profiles[index].Username {
			input.Password = profiles[index].Password
		}
		if input.Password == "" {
			bad(w, 400, "Kamerapasswort erforderlich")
			return
		}
		if index < 0 {
			if len(profiles) >= 16 {
				bad(w, 400, "Höchstens 16 Suchzugänge")
				return
			}
			input.ID = "access-" + token(9)
			profiles = append(profiles, input)
		} else {
			profiles[index] = input
		}
		if err := s.writeCredentialProfiles(profiles); err != nil {
			bad(w, 500, err.Error())
			return
		}
	}
	if r.Method == "DELETE" {
		var input struct {
			ID string `json:"id"`
		}
		if !readReq(w, r, &input) {
			return
		}
		if input.ID == "" && len(profiles) > 1 {
			bad(w, 400, "Zu entfernenden Zugang auswählen")
			return
		}
		keep := []CredentialProfile{}
		found := false
		for _, profile := range profiles {
			if profile.ID == input.ID || input.ID == "" {
				found = true
			} else {
				keep = append(keep, profile)
			}
		}
		if !found && len(profiles) > 0 {
			bad(w, 404, "Suchzugang nicht gefunden")
			return
		}
		profiles = keep
		if err := s.writeCredentialProfiles(profiles); err != nil {
			bad(w, 500, err.Error())
			return
		}
	}
	public := []map[string]any{}
	for _, profile := range profiles {
		public = append(public, map[string]any{"id": profile.ID, "description": profile.Description, "username": profile.Username, "hasPassword": true})
	}
	username := ""
	if len(profiles) > 0 {
		username = profiles[0].Username
	}
	writeJSON(w, 200, map[string]any{"profiles": public, "username": username, "hasPassword": len(profiles) > 0})
}
