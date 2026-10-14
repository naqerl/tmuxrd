@sidebar @switch @performance
Feature: Sidebar reflects window focus quickly and stays consistent
  Switching windows must move the sidebar's ">" selection marker promptly,
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

  Scenario: Navigating switches the view to the next agent
    # j/k must move the *view* (window/session), not just the marker; Enter is
    # what then moves the keyboard into the agent pane.
    Given the sidebar is showing agent rows
    When I select window "0"
    Then the sidebar should select window "0" within 250ms
    When I press the "j" key in the sidebar
    Then window "1" should be the active window of session "s1"
    And the sidebar should select window "1" within 250ms

  Scenario: Navigating back switches the view back
    Given the sidebar is showing agent rows
    When I select window "2"
    Then the sidebar should select window "2" within 250ms
    When I press the "k" key in the sidebar
    Then window "1" should be the active window of session "s1"
    And the sidebar should select window "1" within 250ms

  Scenario: Enter focuses the pointed agent's window
    Given the sidebar is showing agent rows
    When I select window "0"
    Then the sidebar should select window "0" within 250ms
    When I press the "j" key in the sidebar
    Then window "1" should be the active window of session "s1"
    When I press the "Enter" key in the sidebar
    Then window "1" should have its agent pane focused

  Scenario: Focusing the sidebar keeps the window named after its agent
    # tmux renames a window from its active pane, so without pinning the name the
    # window becomes "tmuxrd" the moment the sidebar pane is focused.
    Given the sidebar is showing agent rows
    When I focus the sidebar pane
    And I wait 1200ms
    Then window "0" of session "s1" should be named "pi"

  Scenario: A non-agent window shows no marker
    # The marker must say "the agent you are looking at"; a shell window has no
    # agent in view, so nothing is marked.
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
