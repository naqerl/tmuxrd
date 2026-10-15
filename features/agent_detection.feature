@detection @agents
Feature: Each agent is classified by its own detector

  Detection is a registry of AgentDetector values. Every pane is offered to each
  detector in turn; the first one that claims it names the agent and the state.
  A pane no detector claims is not an agent and never reaches the sidebar — that
  is what keeps a shell, htop or a plain sleep out of the list.

  The grok screens below are verbatim `tmux capture-pane -p` captures of the
  real CLI (grok 1.0.52) running in an isolated tmux server, replayed through
  `fakeagent -screen-file` — the bytes the detector reads are the bytes grok
  drew, not a paraphrase of them.

  Background:
    Given an isolated tmux server "tmuxrd-godog-detect" with sessions:
      | session |
      | s0      |
    And the tmuxrd config is loaded
    And the sidebar is enabled

  Scenario: The fake grok's own frames are read the same way
    # The captures above are the evidence. This keeps the fake's own grok frames
    # honest too — the fake used to draw pi's question for every persona's
    # blocked state — and exercises the state-file channel (a live switch) for
    # grok. Idle comes first: the first run of a working pane sets the finished
    # pin, which would otherwise hide the plain circle.
    Given session "s1" is created with agent windows:
      | agent | state |
      | grok  | idle  |
    When I toggle the sidebar on
    And the sidebar is showing agent rows
    Then the sidebar should show agent "grok" as "○"
    Given agent "grok" in session "s1" window "0" is in state "working"
    Then the sidebar should show agent "grok" as "a braille spinner"
    Given agent "grok" in session "s1" window "0" is in state "blocked"
    Then the sidebar should show agent "grok" as "?"

  Scenario: A real grok idle screen is idle, not an approval
    # grok's footer names the permission mode: "Grok 4.7 (high) · always-approve".
    # The shared keyword rules match "approve", so a grok pane sitting at its
    # prompt used to be reported as blocked; GrokDetector claims grok panes
    # before those rules can see them.
    Given session "s1" is created with agent windows:
      | agent | screen                          |
      | grok  | features/testdata/grok-idle.txt |
    When I toggle the sidebar on
    And the sidebar is showing agent rows
    Then the sidebar should show agent "grok" as "○"

  Scenario: A real grok working frame is working
    Given session "s1" is created with agent windows:
      | agent | screen                             |
      | grok  | features/testdata/grok-working.txt |
    When I toggle the sidebar on
    And the sidebar is showing agent rows
    Then the sidebar should show agent "grok" as "a braille spinner"

  Scenario: A real grok approval card is blocked
    Given session "s1" is created with agent windows:
      | agent | screen                             |
      | grok  | features/testdata/grok-blocked.txt |
    When I toggle the sidebar on
    And the sidebar is showing agent rows
    Then the sidebar should show agent "grok" as "?"

  Scenario: A real grok question card is blocked
    # The question tool's card has no option counter (its rows are "1 (◇) …"),
    # so the approval's footer rule missed it and a live question read as
    # unknown — the pane showed the empty circle while grok waited for an answer.
    Given session "s1" is created with agent windows:
      | agent | screen                              |
      | grok  | features/testdata/grok-question.txt |
    When I toggle the sidebar on
    And the sidebar is showing agent rows
    Then the sidebar should show agent "grok" as "?"

  Scenario: A pane no detector claims is not an agent
    Given session "s1" is created with agent windows:
      | agent | state |
      | plain | idle  |
    When I detect agent panes
    Then detection should find 0 panes
