package controller

import (
	"database/sql"
	"reflect"
	"strconv"
	"strings"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// Only exact prior shipped defaults are upgraded. User edits, withdrawn
// publication state and all installation/job snapshots remain unchanged.
func repairSeededWebDefaults(tx *sql.Tx) error {
	seeds := append(additionalSoftwareDefinitions(), annotationSoftwareDefinitions()...)
	for _, seed := range serviceinstall.BuiltinCommandCatalog() {
		if seed.Key == "nginx" {
			seed.Management = softwareManagementPreset(seed)
			seeds = append(seeds, seed)
		}
	}
	for _, seed := range seeds {
		if seed.Web == nil || seed.DefaultBindAddress != "0.0.0.0" {
			continue
		}
		var id, draft, published string
		var revision int
		if err := tx.QueryRow("SELECT id,draft_json,published_json,revision FROM command_templates WHERE recipe_key=?", commandTemplateKey(seed.Key, seed.Version)).Scan(&id, &draft, &published, &revision); err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return err
		}
		upgrade := func(raw string) (serviceinstall.Definition, bool, error) {
			var actual serviceinstall.Definition
			if raw == "" {
				return actual, false, nil
			}
			if err := deploy.StrictJSON([]byte(raw), &actual); err != nil {
				return actual, false, err
			}
			// Compare the persisted representation: omitted empty slices must not
			// make an untouched generated template look like an administrator edit.
			var current, previous serviceinstall.Definition
			if err := deploy.StrictJSON([]byte(jsonText(seed)), &current); err != nil {
				return actual, false, err
			}
			if err := deploy.StrictJSON([]byte(jsonText(previousWebDefaultDefinition(seed))), &previous); err != nil {
				return actual, false, err
			}
			current.Revision, previous.Revision = actual.Revision, actual.Revision
			if actual.Commands == nil {
				return actual, false, nil
			}
			current = serviceinstall.NormalizeCommandScripts(current)
			current.Commands.Revision = actual.Commands.Revision
			previous.Commands.Revision = actual.Commands.Revision
			if reflect.DeepEqual(actual, current) {
				return actual, false, nil
			}
			matches := reflect.DeepEqual(actual, previous)
			if !matches && strings.HasPrefix(seed.Key, "custom-ytlabel-") {
				// Also recognize the exact intermediate default-only revision.
				previous.DefaultBindAddress, previous.Web = seed.DefaultBindAddress, seed.Web
				matches = reflect.DeepEqual(actual, previous)
			}
			if !matches {
				return actual, false, nil
			}
			return current, true, current.ValidateCommands()
		}
		d, draftChanged, err := upgrade(draft)
		if err != nil {
			return err
		}
		p, publishedChanged, err := upgrade(published)
		if err != nil {
			return err
		}
		if !draftChanged && !publishedChanged {
			continue
		}
		if draftChanged {
			draft = jsonText(d)
		}
		if publishedChanged {
			revision++
			if serviceinstall.IsBuiltinCommand(p) {
				p.Commands.Revision = int64(revision)
			} else {
				p.Revision = strconv.Itoa(revision)
			}
			published = jsonText(p)
		}
		if _, err = tx.Exec("UPDATE command_templates SET draft_json=?,published_json=?,revision=?,version=version+1,updated_at=? WHERE id=?", draft, published, revision, Now(), id); err != nil {
			return err
		}
		if err = auditTx(tx, "SYSTEM", "COMMAND_TEMPLATE_WEB_DEFAULTS_UPDATED", id, "SUCCESS", "template-web-"+id); err != nil {
			return err
		}
	}
	return nil
}

func previousWebDefaultDefinition(seed serviceinstall.Definition) serviceinstall.Definition {
	previous := serviceinstall.NormalizeCommandScripts(seed)
	previous.DefaultBindAddress, previous.Web = "", nil
	if seed.CatalogKey == "label-studio" || seed.CatalogKey == "ytlabel" {
		previous.DefaultBindAddress, previous.Web = "127.0.0.1", seed.Web
	}
	if strings.HasPrefix(seed.Key, "custom-ytlabel-") && previous.Management != nil {
		previous.Management.Parameters = nil
		previous.Management.Operations = previous.Management.Operations[:1]
	}
	for i := range previous.Commands.Steps {
		step := &previous.Commands.Steps[i]
		if strings.HasPrefix(seed.Key, "custom-prometheus-native-") {
			step.Script = strings.ReplaceAll(step.Script, "self_address=$OPS_BIND_ADDRESS\n[[ \"$self_address\" != 0.0.0.0 ]] || self_address=127.0.0.1\n", "")
			step.Script = strings.ReplaceAll(step.Script, "- targets: ['$self_address:$OPS_HOST_PORT']", "- targets: ['$OPS_BIND_ADDRESS:$OPS_HOST_PORT']")
		}
		if strings.HasPrefix(seed.Key, "custom-nacos-native-") {
			step.Script = strings.ReplaceAll(step.Script, "server.address=$OPS_BIND_ADDRESS\n", "server.address=$OPS_BIND_ADDRESS\nnacos.inetutils.ip-address=$OPS_BIND_ADDRESS\n")
			step.Script = strings.ReplaceAll(step.Script, "if [[ \"$OPS_BIND_ADDRESS\" != 0.0.0.0 ]]; then\n  printf 'nacos.inetutils.ip-address=%s\\n' \"$OPS_BIND_ADDRESS\" >> \"$root/config/application.properties\"\nfi\n", "")
		}
	}
	return previous
}
