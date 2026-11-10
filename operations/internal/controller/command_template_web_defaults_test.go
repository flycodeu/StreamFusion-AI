package controller

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestWebDefaultUpgradePreservesEditsPublicationAndHistory(t *testing.T) {
	s, _ := setup(t)
	seeds := append(annotationSoftwareDefinitions(), additionalSoftwareDefinitions()...)
	for _, d := range serviceinstall.BuiltinCommandCatalog() {
		if d.Key == "nginx" {
			d.Management = softwareManagementPreset(d)
			seeds = append(seeds, d)
		}
	}
	type fixture struct {
		seed           serviceinstall.Definition
		previous       string
		key            string
		edited, hidden bool
	}
	fixtures := []fixture{}
	for _, seed := range seeds {
		if seed.Web == nil {
			continue
		}
		previous := previousWebDefaultDefinition(seed)
		if serviceinstall.IsBuiltinCommand(previous) {
			previous.Commands.Revision = 5
		} else {
			previous.Revision = "5"
		}
		edited := seed.Key == "custom-jenkins-native-online" || seed.Key == "custom-label-studio-native-online"
		hidden := seed.Key == "custom-gitlab-docker-offline"
		if edited {
			if seed.Key == "custom-jenkins-native-online" {
				previous.Name = "我的软件配置"
			} else {
				previous.DefaultBindAddress = "192.0.2.17"
			}
		}
		if strings.HasPrefix(seed.Key, "custom-ytlabel-") && (previous.DefaultBindAddress != "127.0.0.1" || len(previous.Management.Operations) != 1 || len(previous.Management.Parameters) != 0) {
			t.Fatal("legacy ytlabel baseline was not preserved")
		}
		if seed.Key == "custom-prometheus-native-online" && (!strings.Contains(previous.Commands.Steps[2].Script, "targets: ['$OPS_BIND_ADDRESS:$OPS_HOST_PORT']") || strings.Contains(previous.Commands.Steps[2].Script, "self_address=")) {
			t.Fatal("legacy Prometheus baseline was not preserved")
		}
		if seed.Key == "custom-nacos-native-online" && !strings.Contains(previous.Commands.Steps[2].Script, "nacos.inetutils.ip-address=$OPS_BIND_ADDRESS") {
			t.Fatal("legacy Nacos baseline was not preserved")
		}
		old := jsonText(previous)
		published := old
		if hidden {
			published = ""
		}
		key := commandTemplateKey(seed.Key, seed.Version)
		if _, err := s.DB.Exec("UPDATE command_templates SET draft_json=?,published_json=?,revision=5,version=8 WHERE recipe_key=?", old, published, key); err != nil {
			t.Fatal(err)
		}
		fixtures = append(fixtures, fixture{seed, old, key, edited, hidden})
	}
	history := schemaSeventeenHistoricalRows(t, s.DB)
	delete(history, "audit")
	var firstAudit int
	for pass := 0; pass < 2; pass++ {
		if err := s.seedSoftwareManagementPresets(); err != nil {
			t.Fatal(err)
		}
		for _, item := range fixtures {
			var draft, published string
			var revision, version int
			if err := s.DB.QueryRow("SELECT draft_json,published_json,revision,version FROM command_templates WHERE recipe_key=?", item.key).Scan(&draft, &published, &revision, &version); err != nil {
				t.Fatal(err)
			}
			if item.edited {
				if draft != item.previous || published != item.previous || revision != 5 || version != 8 {
					t.Fatal("user edit was replaced", item.key)
				}
				continue
			}
			var d serviceinstall.Definition
			if json.Unmarshal([]byte(draft), &d) != nil || d.DefaultBindAddress != "0.0.0.0" || d.Web == nil || d.ValidateCommands() != nil {
				t.Fatal("shipped Web default was not upgraded", item.key)
			}
			if strings.HasPrefix(d.Key, "custom-ytlabel-") && len(d.Management.Operations) != 4 {
				t.Fatal("ytlabel network management was not included", item.key)
			}
			if item.hidden {
				if published != "" || revision != 5 || version != 9 {
					t.Fatal("withdrawn template was republished", item.key)
				}
			} else {
				if json.Unmarshal([]byte(published), &d) != nil || d.ValidateCommands() != nil || revision != 6 || version != 9 {
					t.Fatal("published upgrade or revision invalid", item.key)
				}
			}
		}
		after := schemaSeventeenHistoricalRows(t, s.DB)
		delete(after, "audit")
		if !reflect.DeepEqual(history, after) {
			t.Fatal("Web default upgrade changed historical records")
		}
		var audits int
		if err := s.DB.QueryRow("SELECT count(*) FROM audit WHERE action='COMMAND_TEMPLATE_WEB_DEFAULTS_UPDATED'").Scan(&audits); err != nil || audits == 0 {
			t.Fatal("missing upgrade audit", audits, err)
		}
		if pass == 0 {
			firstAudit = audits
		} else if audits != firstAudit {
			t.Fatal("Web default upgrade ran twice")
		}
	}
}
