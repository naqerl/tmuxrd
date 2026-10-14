@sidebar @toggle
Feature: Sidebar visibility is toggled atomically across every window
  As a heavy tmux user I want one global toggle that shows or hides the
  sidebar in every window of every session, and never leaves a half-state.

  Scenario: Turning the sidebar on gives every window exactly one sidebar
    Given an isolated tmux server "tmuxrd-godog-a" with sessions:
      | session |
      | s1      |
    And session "s1" has windows:
      | window |
      | 0      |
      | 1      |
      | 2      |
    When I toggle the sidebar on
    Then every window should have exactly one sidebar pane
    And the sidebar count should be 3

  Scenario: Turning the sidebar off removes all of them
    Given an isolated tmux server "tmuxrd-godog-a" with sessions:
      | session |
      | s1      |
    And session "s1" has windows:
      | window |
      | 0      |
      | 1      |
    When I toggle the sidebar on
    And I toggle the sidebar off
    Then the sidebar count should be 0
    And every window should have no sidebar pane

  Scenario: Toggling is idempotent and never double-creates
    Given an isolated tmux server "tmuxrd-godog-a" with sessions:
      | session |
      | s1      |
    And session "s1" has windows:
      | window |
      | 0      |
      | 1      |
    When I toggle the sidebar on
    And I toggle the sidebar on
    And I toggle the sidebar on
    Then every window should have exactly one sidebar pane
    And the sidebar count should be 2

  Scenario: Sidebar survives across multiple sessions
    Given an isolated tmux server "tmuxrd-godog-a" with sessions:
      | session |
      | alpha   |
      | beta    |
    And session "alpha" has windows:
      | window |
      | 0      |
      | 1      |
    And session "beta" has windows:
      | window |
      | 0      |
    When I toggle the sidebar on
    Then every window should have exactly one sidebar pane
    And the sidebar count should be 3
