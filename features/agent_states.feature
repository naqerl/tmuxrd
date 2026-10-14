@agent-states @sidebar
Feature: The sidebar renders each agent's state with the right glyph
  fakeagent's state API (-state flag, positional arg, and the polled -state
  file) lets a scenario pin every pane to a known state, so the sidebar can be
  asserted exactly: working = an animated braille spinner, blocked = "?",
  idle/unknown = "○".

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

  Scenario: An idle agent shows the "○" glyph
    Given session "s1" is created with agent windows:
      | agent | state |
      | pi    | idle  |
    When I toggle the sidebar on
    And the sidebar is showing agent rows
    Then the sidebar should show agent "pi" as "○"

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
