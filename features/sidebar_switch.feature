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

  Scenario: The marker does not snap back to the focused window
    # The daemon pushes state every 500ms. Re-syncing the cursor on every push
    # wiped a manual selection, so j/k appeared to do nothing.
    Given the sidebar is showing agent rows
    When I select window "2"
    Then the sidebar should select window "2" within 250ms
    When I press the "k" key in the sidebar
    Then the sidebar should select window "1" within 250ms
    When I wait 1600ms
    Then the sidebar should select window "1" within 250ms

  Scenario: Enter focuses the pointed agent's window
    Given the sidebar is showing agent rows
    When I select window "0"
    Then the sidebar should select window "0" within 250ms
    When I press the "j" key in the sidebar
    Then the sidebar should select window "1" within 250ms
    When I press the "Enter" key in the sidebar
    Then window "1" should be the active window of session "s1"

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
