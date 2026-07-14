package client

func DriftResourcesFromStepResult(result map[string]any) []DriftResourceDetail {
	if result == nil {
		return nil
	}
	tasks, isList := result["tasks"].([]any)
	if !isList {
		return nil
	}
	driftResources := []DriftResourceDetail{}
	for _, taskRaw := range tasks {
		task, isMap := taskRaw.(map[string]any)
		if !isMap {
			continue
		}
		jsonOutput, isJSONOutput := task["json_output"].(map[string]any)
		if !isJSONOutput {
			continue
		}
		detail, isDetail := jsonOutput["detail"].(map[string]any)
		if !isDetail {
			continue
		}
		driftEntries, isDriftList := detail["drift_resources"].([]any)
		if !isDriftList {
			continue
		}
		for _, driftEntryRaw := range driftEntries {
			driftEntry, isDriftMap := driftEntryRaw.(map[string]any)
			if !isDriftMap {
				continue
			}
			parsed := DriftResourceDetail{
				APIVersion: stringField(driftEntry, "api_version"),
				Kind:       stringField(driftEntry, "kind"),
				Namespace:  stringField(driftEntry, "namespace"),
				Name:       stringField(driftEntry, "name"),
				DriftType:  stringField(driftEntry, "drift_type"),
				Paths:      stringListField(driftEntry, "paths"),
			}
			if parsed.Kind != "" && len(parsed.Paths) > 0 {
				driftResources = append(driftResources, parsed)
			}
		}
	}
	if len(driftResources) == 0 {
		return nil
	}
	return driftResources
}

func stringField(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func stringListField(values map[string]any, key string) []string {
	rawList, isList := values[key].([]any)
	if !isList {
		return nil
	}
	members := make([]string, 0, len(rawList))
	for _, rawMember := range rawList {
		member, isString := rawMember.(string)
		if isString {
			members = append(members, member)
		}
	}
	return members
}

func (client *Client) EnrichExecutionDetailWithDrift(detail *ExecutionDetail) error {
	if detail == nil {
		return nil
	}
	resultResponse, resultError := client.GetExecutionResult(detail.Execution.ID)
	if resultError != nil {
		return resultError
	}
	driftByStep := map[string][]DriftResourceDetail{}
	for _, stepResult := range resultResponse.Results {
		driftResources := DriftResourcesFromStepResult(stepResult.Result)
		if len(driftResources) > 0 {
			driftByStep[stepResult.StepID] = driftResources
		}
	}
	for stepIndex := range detail.Steps {
		if driftResources, hasDrift := driftByStep[detail.Steps[stepIndex].ID]; hasDrift {
			detail.Steps[stepIndex].DriftResources = driftResources
		}
	}
	return nil
}
