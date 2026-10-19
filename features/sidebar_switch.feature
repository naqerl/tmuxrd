@sidebar @switch @performance
Feature: Sidebar reflects window focus quickly and stays consistent
  Switching windows must move the sidebar's accent highlight promptly,
  and the sidebar must never become inconsistent (double / missing panes).

  Background:
    Given an isolated tmux server "tmuxrd-godog-d"
    And session "s1" is created with agent windows:
      | agent  |
      | pi     |
      | claude |
      | codex  |
    And the tmuxrd config is loaded
    And the sidebar is enabled
    When I toggle the sidebar on

  Scenario: Every window has exactly one sidebar after enabling
    Then every window should have exactly one sidebar pane
    And the sidebar count should be 3

  Scenario: Selecting each window shows it as selected promptly
    # The daemon polls every 500ms, so meeting a sub-500ms budget proves the
    # selection arrives via the after-select-window hook's notify, not by poll.
    Given the sidebar is showing agent rows
    When I select window "0"
    Then the sidebar should select window "0" within 250ms
    When I select window "1"
    Then the sidebar should select window "1" within 250ms
    When I select window "2"
    Then the sidebar should select window "2" within 250ms

  Scenario: The marker survives focus moving to the sidebar itself
    Given the sidebar is showing agent rows
    When I focus the sidebar pane
    Then the sidebar should still show a selection marker

  Scenario: j switches to the next agent and keeps the sidebar focused
    # j/k move the view and leave the keyboard on that window's sidebar, so the
    # next press still lands in tmuxrd. Enter puts the keyboard in the agent.
    Given the sidebar is showing agent rows
    When I select window "0"
    Then the sidebar should select window "0" within 250ms
    When I press the "j" key in the sidebar
    Then the sidebar should select window "1" within 250ms
    And window "1" should be the active window of session "s1"
    And window "1" of session "s1" should have its sidebar pane focused

  Scenario: k switches back to the previous agent and keeps the sidebar focused
    Given the sidebar is showing agent rows
    When I select window "2"
    Then the sidebar should select window "2" within 250ms
    When I press the "k" key in the sidebar
    Then the sidebar should select window "1" within 250ms
    And window "1" should be the active window of session "s1"
    And window "1" of session "s1" should have its sidebar pane focused

  Scenario: Enter focuses the pointed agent's pane
    Given the sidebar is showing agent rows
    When I select window "0"
    Then the sidebar should select window "0" within 250ms
    When I press the "j" key in the sidebar
    When I press the "Enter" key in the sidebar
    Then window "1" should be the active window of session "s1"
    And window "1" should have its agent pane focused

  Scenario: Focusing the sidebar keeps the window named after its agent
    # tmux renames a window from its active pane, so without pinning the name the
    # window becomes "tmuxrd" the moment the sidebar pane is focused.
    Given the sidebar is showing agent rows
    When I focus the sidebar pane
    And I wait 1200ms
    Then window "0" of session "s1" should be named "pi"

  Scenario: An empty marker picks the running agent and stays there
    # A shell window has no agent of its own. Clearing the marker made the
    # next key start at the wrong row, and the following snapshot wiped it.
    # The empty marker picks the agent that is working, and j moves the view
    # there instead of snapping back to nothing.
    Given agent "claude" in session "s1" window "1" is in state "working"
    And the sidebar should show a timer for agent "claude"
    And session "s1" has an extra non-agent window
    When I select window "3"
    Then the sidebar should select window "1" within 500ms
    When I wait 1200ms
    Then the sidebar should select window "1" within 250ms
    When I press the "j" key in the sidebar
    Then window "1" should be the active window of session "s1"
    And window "1" of session "s1" should have its sidebar pane focused

  Scenario: A non-agent window shows no marker when nothing is working
    # With no working agent there is nothing to fall back to, so a shell
    # window shows no marker.
    Given the sidebar is showing agent rows
    And session "s1" has an extra non-agent window
    When I select window "3"
    Then the sidebar should show no selection marker
    When I select window "1"
    Then the sidebar should select window "1" within 250ms

  Scenario: Sidebar stays consistent after rapid window switching
    When I select window "0"
    And I select window "1"
    And I select window "2"
    And I select window "0"
    Then every window should have exactly one sidebar pane
    And the sidebar count should be 3

  Scenario: Sidebar content lists each session's agents
    And I refresh the sidebar
    Then the sidebar should be visible
    And the sidebar content should mention agent "pi"
    And directory names should be marked with a trailing slash
