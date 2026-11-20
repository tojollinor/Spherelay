package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
)

var trustedInstallerComponents = map[string]string{"ffmpeg": "3256173f3f8bffd7df12227c68adf68025edb1832273a9530688a7bb1ed8edec", "mediamtx": "13a0bfaa852d83ce976c94342a1af130941bd79fac2c1e95a2175879c92304c0"}

func installerArg(args []string, key string) string {
	for i, a := range args {
		if a == key && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
func decodeInstallerPassword(raw []byte) string {
	if len(raw) >= 2 && raw[0] == 255 && raw[1] == 254 {
		units := make([]uint16, (len(raw)-2)/2)
		for i := range units {
			units[i] = binary.LittleEndian.Uint16(raw[2+i*2:])
		}
		return string(utf16.Decode(units))
	}
	return strings.TrimPrefix(string(raw), "\ufeff")
}
func provisionInstaller(s *Server, password string, lan, streams bool) error {
	if s.store.PasswordHash == "" {
		if len([]rune(password)) < 12 {
			return fmt.Errorf("Administratorpasswort muss mindestens 12 Zeichen enthalten")
		}
		s.store.Salt = hex.EncodeToString(randBytes(16))
		s.store.PasswordHash = hashPassword(s.store.Salt, password)
	}
	for _, c := range s.store.Cameras {
		if c.Secret != "" {
			if _, err := unprotectSecret(c.Secret); err != nil {
				return fmt.Errorf("Kamerazugänge sind auf diesem PC nicht entschlüsselbar; bitte ohne Datenübernahme installieren")
			}
		}
	}
	if _, err := s.readCredentialProfiles(); err != nil {
		return err
	}
	s.store.NetworkRTSP = lan
	s.store.NetworkUI = false
	for i := range s.store.Cameras {
		s.store.Cameras[i].AutoStart = false
	}
	if streams {
		for i, c := range s.store.Cameras {
			if !c.Enabled {
				continue
			}
			for _, v := range s.store.Streams {
				if v.CameraID == c.ID && v.Enabled {
					s.store.Cameras[i].AutoStart = true
					break
				}
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}
func installerComponent(s *Server, name string) error {
	expected, ok := trustedInstallerComponents[name]
	if !ok {
		return fmt.Errorf("Unbekannte Videokomponente")
	}
	path := filepath.Join(s.binDir, name+".exe")
	if raw, err := os.ReadFile(path); err == nil {
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) == expected {
			fmt.Println(name + ": vorhandene geprüfte Programmdatei übernommen")
			return nil
		}
		if err = os.Rename(path, path+".unverified"); err != nil {
			return err
		}
	}
	s.components = map[string]*ComponentStatus{name: {Name: name, Status: "installing"}}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		var rate transferRate
		for {
			select {
			case <-done:
				return
			case now := <-ticker.C:
				s.mu.Lock()
				st := *s.components[name]
				s.mu.Unlock()
				suffix := ""
				if st.Phase == "Wird heruntergeladen" {
					if speed, ok := rate.sample(now, st.Received); ok {
						suffix = fmt.Sprintf(" [%.2f MB/s]", speed)
					}
				} else {
					rate.samples = nil
				}
				if st.Total > 0 {
					fmt.Printf("%s: %s (%d / %d MB)%s\n", name, st.Phase, st.Received>>20, st.Total>>20, suffix)
				} else if st.Phase == "Wird heruntergeladen" {
					fmt.Printf("%s: %s (%d MB geladen)%s\n", name, st.Phase, st.Received>>20, suffix)
				} else {
					fmt.Printf("%s: %s\n", name, st.Phase)
				}
			}
		}
	}()
	err := s.fetchComponent(name)
	close(done)
	return err
}
func handleInstallerCommand(args []string, data string) bool {
	provision := hasArgument(args, "--installer-provision")
	component := installerArg(args, "--installer-component")
	if !provision && component == "" {
		return false
	}
	s, err := newServer(data)
	if err == nil && provision {
		var password string
		if file := installerArg(args, "--setup-password-file"); file != "" {
			var raw []byte
			raw, err = os.ReadFile(file)
			if err == nil {
				password = decodeInstallerPassword(raw)
			}
		}
		if err == nil {
			err = provisionInstaller(s, password, installerArg(args, "--lan") == "1", installerArg(args, "--start-streams") == "1")
		}
		password = ""
	}
	if err == nil && component != "" {
		err = installerComponent(s, component)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Einrichtung fehlgeschlagen:", err)
		os.Exit(1)
	}
	fmt.Println("Einrichtungsschritt erfolgreich")
	return true
}
