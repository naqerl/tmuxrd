@agent-states @sidebar
Feature: The sidebar renders each agent's state with the right glyph
  fakeagent's state API (-state flag, positional arg, and the polled -state
  file) lets a scenario pin every pane to a known state, so the sidebar can be
  asserted exactly: working = an animated braille spinner, blocked = "?",
  finished (worked, then stopped) = "✓", never-worked = "○". A `screen` column
  instead replays a captured pane verbatim (fakeagent -screen-file), so a
  scenario can assert against exactly what a real agent TUI rendered.

  Background:
    Given an isolated tmux server "tmuxrd-godog-f" with sessions:
      | session |
      | s0      |
    And the tmuxrd config is loaded
    And the sidebar is enabled

  Scenario: A working agent shows an animated braille spinner
    Given session "s1" is created with agent windows:
      | agent | state   |
      | pi    | working |
    When I toggle the sidebar on
    And the sidebar is showing agent rows
    Then the sidebar should show agent "pi" as "a braille spinner"
    # the spinner animates at spinner.MiniDot.FPS, so distinct frames must
    # appear across ~1.5s of captures (a static glyph would fail this)
    And I capture the sidebar 8 times over 1500ms
    And at least 2 distinct spinner frames should have been captured

  Scenario: A blocked agent shows the "?" glyph
    Given session "s1" is created with agent windows:
      | agent | state   |
      | pi    | blocked |
    When I toggle the sidebar on
    And the sidebar is showing agent rows
    Then the sidebar should show agent "pi" as "?"

  Scenario: pi's real single-select question TUI (captured) is blocked
    # features/testdata/pi-question-2026-10-15.txt is a verbatim
    # `tmux capture-pane -p` of a live pi question (extension
    # @juicesharp/rpiv-ask-user-question 2.12.0): the options are plain
    # "N. label" rows — a single-select question has no "[ ]" checkbox — and
    # the footer carries "Tab to switch questions" but no "Space to toggle".
    # tmuxrd reported this as unknown (the matcher wanted the multi-select
    # footer plus numbered "[ ]" rows), so this scenario pins the fix against
    # exactly what pi put on screen.
    Given session "s1" is created with agent windows:
      | agent | screen                                       |
      | pi    | features/testdata/pi-question-2026-10-15.txt |
    When I toggle the sidebar on
    And the sidebar is showing agent rows
    Then the sidebar should show agent "pi" as "?"

  Scenario: An idle agent that never started shows the "○" glyph
    Given session "s1" is created with agent windows:
      | agent | state |
      | pi    | idle  |
    When I toggle the sidebar on
    And the sidebar is showing agent rows
    Then the sidebar should show agent "pi" as "○"

  Scenario: An agent that finished working shows a checkmark
    Given session "s1" is created with agent windows:
      | agent | state   |
      | pi    | working |
    When I toggle the sidebar on
    And the sidebar is showing agent rows
    # Waiting for the spinner proves the daemon has seen the run: it records it
    # as the pane's @tmuxrd_finished option, which is what turns the glyph into
    # ✓ once the agent stops. Without the marker an idle pane is only ○.
    Then the sidebar should show agent "pi" as "a braille spinner"
    Given agent "pi" in session "s1" window "0" is in state "idle"
    Then the sidebar should show agent "pi" as "✓"

  Scenario: The checkmark survives a daemon restart
    Given session "s1" is created with agent windows:
      | agent | state   |
      | pi    | working |
    When I toggle the sidebar on
    And the sidebar is showing agent rows
    Then the sidebar should show agent "pi" as "a braille spinner"
    Given agent "pi" in session "s1" window "0" is in state "idle"
    Then the sidebar should show agent "pi" as "✓"
    # The marker is a tmux pane option, so a fresh daemon (an upgrade, a crash)
    # reads it back from the pane instead of resetting every agent to "new".
    When I restart the daemon
    And the sidebar is showing agent rows
    Then the sidebar should show agent "pi" as "✓"

  Scenario: The checkmark clears once the agent's window is viewed
    Given session "s1" is created with agent windows:
      | agent | state   |
      | pi    | working |
    When I toggle the sidebar on
    And the sidebar is showing agent rows
    Then the sidebar should show agent "pi" as "a braille spinner"
    Given agent "pi" in session "s1" window "0" is in state "idle"
    Then the sidebar should show agent "pi" as "✓"
    # So far no tmux client was attached, so nothing had been seen and the pin
    # survived. A client makes s1's current window (the agent's) viewed, and the
    # daemon consumes the marker on its next tick — the ✓ is a notification, not
    # a permanent badge.
    When a client is attached to session "s1"
    Then the sidebar should show agent "pi" as "○"

  Scenario: An agent that finishes in the window you watch does not pin
    # The client is attached before the agent's window exists, so the daemon
    # never sees this agent working unviewed: no marker is ever written, and the
    # run you watched ends on the plain circle rather than a checkmark.
    Given session "s1" is created with agent windows:
      | agent | state |
      | plain | idle  |
    And a client is attached to session "s1"
    And session "s1" has window "1" running agent "pi" in state "working"
    When I toggle the sidebar on
    And the sidebar is showing agent rows
    Then the sidebar should show agent "pi" as "a braille spinner"
    Given agent "pi" in session "s1" window "1" is in state "idle"
    Then the sidebar should show agent "pi" as "○"

  Scenario: A never-worked agent keeps the empty circle next to a finished one
    Given session "s1" is created with agent windows:
      | agent | state   |
      | pi    | working |
    And session "s1" has window "1" running agent "claude" in state "idle"
    When I toggle the sidebar on
    And the sidebar is showing agent rows
    Then the sidebar should show agent "pi" as "a braille spinner"
    Given agent "pi" in session "s1" window "0" is in state "idle"
    Then the sidebar should show agent "pi" as "✓"
    And the sidebar should show agent "claude" as "○"

  Scenario: Switching a live agent's state changes its glyph
    Given session "s1" is created with agent windows:
      | agent | state |
      | pi    | idle  |
    When I toggle the sidebar on
    And the sidebar is showing agent rows
    Then the sidebar should show agent "pi" as "○"
    # the pane keeps running; only its state file changes
    Given agent "pi" in session "s1" window "0" is in state "blocked"
    Then the sidebar should show agent "pi" as "?"
    Given agent "pi" in session "s1" window "0" is in state "working"
    Then the sidebar should show agent "pi" as "a braille spinner"

  Scenario: A mixed sidebar shows every agent with its own correct glyph
    Given session "s1" is created with agent windows:
      | agent | state   |
      | pi    | working |
      | claude | blocked |
      | codex  | idle    |
      | kimi   | unknown |
    When I toggle the sidebar on
    And the sidebar is showing agent rows
    Then the sidebar should show agent "pi" as "a braille spinner"
    And the sidebar should show agent "claude" as "?"
    And the sidebar should show agent "codex" as "○"
    And the sidebar should show agent "kimi" as "○"

  Scenario: A working agent shows a timer that runs
    Given session "s1" is created with agent windows:
      | agent | state   |
      | pi    | working |
    When I toggle the sidebar on
    And the sidebar is showing agent rows
    Then the sidebar should show a timer for agent "pi"
    And the timer for agent "pi" should increase within 5000ms

  Scenario: The timer stops when the agent stops working
    Given session "s1" is created with agent windows:
      | agent | state   |
      | pi    | working |
    When I toggle the sidebar on
    And the sidebar is showing agent rows
    And I wait 1500ms
    And agent "pi" in session "s1" window "0" is in state "idle"
    Then the timer for agent "pi" should stop within 6000ms

  Scenario: The timer resets when the agent starts working again
    Given session "s1" is created with agent windows:
      | agent | state   |
      | pi    | working |
    When I toggle the sidebar on
    And the sidebar is showing agent rows
    And I wait 2500ms
    And agent "pi" in session "s1" window "0" is in state "idle"
    And I wait 1200ms
    And agent "pi" in session "s1" window "0" is in state "working"
    Then the timer for agent "pi" should reset within 5000ms
