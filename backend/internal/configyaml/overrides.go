package configyaml

import (
	"bytes"
	"fmt"
	"gopkg.in/yaml.v3"
)

// MergeOverrides preserves subscription nodes and comments. Only TUN is merged
// per field; other top-level values (including explicit empty lists) replace it.
func MergeOverrides(raw []byte, overrides map[string]any) ([]byte, error) {
	if len(overrides) == 0 {
		return raw, nil
	}
	var document yaml.Node
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("配置必须为 YAML 对象")
	}
	root := document.Content[0]
	var patch yaml.Node
	if err := patch.Encode(overrides); err != nil {
		return nil, err
	}
	for i := 0; i < len(patch.Content); i += 2 {
		key, value := patch.Content[i], patch.Content[i+1]
		index := -1
		for j := 0; j < len(root.Content); j += 2 {
			if root.Content[j].Value == key.Value {
				index = j
				break
			}
		}
		if index < 0 {
			root.Content = append(root.Content, key, value)
			continue
		}
		if key.Value == "tun" && value.Kind == yaml.MappingNode {
			var base map[string]any
			if err := root.Content[index+1].Decode(&base); err != nil {
				return nil, err
			}
			if base == nil {
				base = map[string]any{}
			}
			var changes map[string]any
			if err := value.Decode(&changes); err != nil {
				return nil, err
			}
			for k, v := range changes {
				base[k] = v
			}
			if err := value.Encode(base); err != nil {
				return nil, err
			}
		}
		previous := root.Content[index+1]
		value.HeadComment = previous.HeadComment
		value.Anchor = previous.Anchor
		*previous = *value
	}
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return nil, err
	}
	return output.Bytes(), encoder.Close()
}
