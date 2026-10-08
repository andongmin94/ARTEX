package agent

// scopeOverview exposes the existing read-only scope without changing membership.
func (t *ToolSet) scopeOverview() []map[string]any {
	if t.as == nil || t.taskID <= 0 {
		return nil
	}
	rows, err := t.as.ListTaskScopeWithSources(t.taskID)
	if err != nil {
		return nil
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		item := map[string]any{"id": row.ID, "kind": row.Kind, "domain": row.Domain, "net": row.Net, "value": row.Value, "reason": row.Reason, "source": row.Source}
		if row.TaskID != t.taskID {
			item["source_task_id"] = row.TaskID
			item["inherited"] = true
		}
		if row.CompanyID != nil {
			item["company_id"] = *row.CompanyID
			item["company_name"] = row.CompanyName
			if rules, err := t.as.Companies().GetScope(*row.CompanyID); err == nil {
				details := make([]map[string]any, 0, len(rules))
				keywords := []string{}
				for _, rule := range rules {
					details = append(details, map[string]any{"kind": rule.Kind, "raw": rule.Raw, "domain": rule.Domain, "net": rule.Net, "value": rule.Raw, "reason": rule.Reason})
					if rule.Kind == "keyword" {
						keywords = append(keywords, rule.Raw)
					}
				}
				item["company_scope"] = details
				item["company_keywords"] = keywords
			}
		}
		out = append(out, item)
	}
	return out
}
