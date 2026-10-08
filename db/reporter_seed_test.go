package db

import "testing"

func TestSQLiteReporterSeedAtomicAndPreservesEdits(t *testing.T) {
	d := openBusinessFixture(t, testDSN(t))
	keys := []string{"update_finding_report", "get_task_node_detail", "list_task_findings", "get_task_worker_trace", "list_task_worker_traces", "search_task_worker_traces", "get_task_graph", "get_finding_traffic", "bind_finding_traffic", "traffic_search", "traffic_get", "traffic_blob"}
	for _, key := range keys[:len(keys)-1] {
		if _, err := d.Exec(`INSERT INTO tools(key) VALUES(?1)`, key); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.SeedReporter(t.Context(), "initial prompt", "initial trigger"); err == nil {
		t.Fatal("missing required tool must fail")
	}
	var agents, prompts, marker int
	if err := d.QueryRow(`SELECT (SELECT count(*) FROM agents WHERE key='reporter'),(SELECT count(*) FROM agent_prompts p JOIN agents a ON a.id=p.agent_id WHERE a.key='reporter'),(SELECT count(*) FROM settings WHERE key='reporter_agent_seed_v1')`).Scan(&agents, &prompts, &marker); err != nil {
		t.Fatal(err)
	}
	if agents != 0 || prompts != 0 || marker != 0 {
		t.Fatalf("partial seed survived rollback: %d %d %d", agents, prompts, marker)
	}
	if _, err := d.Exec(`INSERT INTO tools(key) VALUES('traffic_blob')`); err != nil {
		t.Fatal(err)
	}
	if err := d.SeedReporter(t.Context(), "initial prompt", "initial trigger"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE agents SET name='사용자 보고서',enabled=false WHERE key='reporter'`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE agent_prompts SET template_text='사용자 편집' WHERE agent_id=(SELECT id FROM agents WHERE key='reporter')`); err != nil {
		t.Fatal(err)
	}
	if err := d.SeedReporter(t.Context(), "replacement prompt", "replacement trigger"); err != nil {
		t.Fatal(err)
	}
	var name, prompt string
	var enabled bool
	if err := d.QueryRow(`SELECT a.name,a.enabled,p.template_text FROM agents a JOIN agent_prompts p ON p.id=a.current_prompt_id WHERE a.key='reporter'`).Scan(&name, &enabled, &prompt); err != nil {
		t.Fatal(err)
	}
	if name != "사용자 보고서" || enabled || prompt != "사용자 편집" {
		t.Fatalf("user edit overwritten: %q %t %q", name, enabled, prompt)
	}
	if _, err := d.Exec(`DELETE FROM agents WHERE key='reporter'`); err != nil {
		t.Fatal(err)
	}
	if err := d.SeedReporter(t.Context(), "replacement prompt", "replacement trigger"); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM agents WHERE key='reporter'`).Scan(&agents); err != nil {
		t.Fatal(err)
	}
	if agents != 0 {
		t.Fatal("deliberately deleted reporter was recreated")
	}
}
