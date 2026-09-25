// Command agentjudge runs the turn judge of internal/agentjudge offline over
// turns given on stdin, so evals score dialogues with the same specs and code
// criteria the runtime uses, without Langfuse.
//
// Input (stdin, JSON):
//
//	{"spec_dir": "agents/tg-exchange-support/judge",
//	 "turns": [{"id": "t01/2", "username": "eval_t01", "incoming": ["..."], "reply": "...",
//	            "parts": ["..."], "history": [{"role": "client", "text": "..."}],
//	            "state": {"reported_facts": {}, "open_loops": []},
//	            "kb": [{"query": "...", "text": "..."}],
//	            "no_question_this_turn": false, "reply_error": false}]}
//
// Output (stdout, JSON): {"specs": [{"name": "turn", "fail_below": 50}],
// "turns": [{"id": "t01/2", "results": {"turn": [{name, value, boolean, comment, severity}]}, "errors": {"turn": "..."}}]}.
//
// LLM criteria run when AGENT_JUDGE_LLM_URL, AGENT_JUDGE_LLM_KEY and
// AGENT_JUDGE_LLM_MODEL are set; otherwise only code criteria run and the
// output says llm: "off". Skip lists of the specs are ignored: evals are the
// traffic the specs skip in production.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/dada-tuda/console/backend/internal/agentjudge"
)

type inputTurn struct {
	ID                 string          `json:"id"`
	Username           string          `json:"username"`
	Incoming           []string        `json:"incoming"`
	Reply              string          `json:"reply"`
	Parts              []string        `json:"parts"`
	History            []exchange      `json:"history"`
	State              json.RawMessage `json:"state"`
	NoQuestionThisTurn bool            `json:"no_question_this_turn"`
	ReplyError         bool            `json:"reply_error"`
	KB                 []kbResult      `json:"kb"`
}

type kbResult struct {
	Query string `json:"query"`
	Text  string `json:"text"`
}

type exchange struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type input struct {
	SpecDir string      `json:"spec_dir"`
	Turns   []inputTurn `json:"turns"`
}

type outResult struct {
	Name     string  `json:"name"`
	Value    float64 `json:"value"`
	Boolean  bool    `json:"boolean"`
	Comment  string  `json:"comment,omitempty"`
	Severity string  `json:"severity,omitempty"`
}

type outTurn struct {
	ID      string                 `json:"id"`
	Results map[string][]outResult `json:"results"`
	Errors  map[string]string      `json:"errors,omitempty"`
}

type outSpec struct {
	Name      string  `json:"name"`
	FailBelow float64 `json:"fail_below"`
	Total     string  `json:"total"`
	LLM       int     `json:"llm_criteria"`
	Code      int     `json:"code_criteria"`
}

type output struct {
	LLM   string    `json:"llm"`
	Specs []outSpec `json:"specs"`
	Turns []outTurn `json:"turns"`
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "agentjudge:", err)
		os.Exit(1)
	}
}

func run(in io.Reader, out io.Writer) error {
	var req input
	if err := json.NewDecoder(in).Decode(&req); err != nil {
		return fmt.Errorf("decode input: %w", err)
	}
	if req.SpecDir == "" {
		return fmt.Errorf("spec_dir is required")
	}
	specs, err := agentjudge.LoadSpecs(req.SpecDir)
	if err != nil {
		return fmt.Errorf("load specs %s: %w", req.SpecDir, err)
	}
	var llm agentjudge.LLM
	res := output{LLM: "off"}
	url, key, model := os.Getenv("AGENT_JUDGE_LLM_URL"), os.Getenv("AGENT_JUDGE_LLM_KEY"), os.Getenv("AGENT_JUDGE_LLM_MODEL")
	if url != "" && key != "" && model != "" {
		llm = &agentjudge.OpenAIChat{BaseURL: url, APIKey: key, Model: model}
		res.LLM = model
	}
	judge := agentjudge.New("", llm, nil)
	for _, s := range specs {
		llmN, codeN := 0, 0
		for _, c := range s.Criteria {
			if c.Check == agentjudge.CheckLLM {
				llmN++
			} else {
				codeN++
			}
		}
		res.Specs = append(res.Specs, outSpec{Name: s.Name, FailBelow: s.Total.FailBelow, Total: s.ScoreName(s.Total.Score), LLM: llmN, Code: codeN})
	}
	ctx := context.Background()
	for _, it := range req.Turns {
		t := agentjudge.Turn{Username: it.Username, Incoming: it.Incoming, Reply: it.Reply, Parts: it.Parts,
			NoQuestionThisTurn: it.NoQuestionThisTurn, ReplyError: it.ReplyError}
		for _, h := range it.History {
			t.History = append(t.History, agentjudge.Exchange{Role: h.Role, Text: h.Text})
		}
		for _, k := range it.KB {
			t.KB = append(t.KB, agentjudge.KBResult{Query: k.Query, Text: k.Text})
		}
		t.Context = contextJSON(it)
		ot := outTurn{ID: it.ID, Results: map[string][]outResult{}}
		for _, s := range specs {
			results, rerr := judge.Run(ctx, s, t)
			rows := make([]outResult, 0, len(results))
			for _, r := range results {
				rows = append(rows, outResult{Name: r.Name, Value: r.Value, Boolean: r.Boolean, Comment: r.Comment, Severity: r.Severity})
			}
			ot.Results[s.Name] = rows
			if rerr != nil {
				if ot.Errors == nil {
					ot.Errors = map[string]string{}
				}
				ot.Errors[s.Name] = rerr.Error()
			}
		}
		res.Turns = append(res.Turns, ot)
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", " ")
	return enc.Encode(res)
}

func contextJSON(it inputTurn) string {
	state := map[string]any{}
	if len(it.State) > 0 {
		_ = json.Unmarshal(it.State, &state)
	}
	ctx := map[string]any{"reported_facts": state["reported_facts"], "open_loops": state["open_loops"],
		"no_question_this_turn": it.NoQuestionThisTurn, "reply_error": it.ReplyError, "username": it.Username}
	b, _ := json.Marshal(ctx)
	return string(b)
}
