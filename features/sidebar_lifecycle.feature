@lifecycle @windows
Feature: Sidebar lifecycle follows window and pane events
  The sidebar must appear when new windows are created (after-new-window) and
  disappear cleanly when its host pane dies — pane consistency under churn.

  Scenario: A new window created while enabled gets a sidebar automatically
    Given an isolated tmux server "tmuxrd-godog-b" with sessions:
      | session |
      | s1      |
    And session "s1" has windows:
      | window |
      | 0      |
    And the sidebar is enabled
    When I toggle the sidebar on
    And a new window is created in session "s1"
    And I ensure sidebar for new windows
    Then every window should have exactly one sidebar pane
    And the sidebar count should be 2

  Scenario: A new window created while disabled stays sidebar-free
    Given an isolated tmux server "tmuxrd-godog-b" with sessions:
      | session |
      | s1      |
    And session "s1" has windows:
      | window |
      | 0      |
    And the sidebar is disabled
    When I ensure sidebar for new windows
    Then the sidebar count should be 0

  Scenario: Killing the main pane orphans and then reaps the sidebar
    Given an isolated tmux server "tmuxrd-godog-b" with sessions:
      | session |
      | s1      |
    And session "s1" has windows:
      | window |
      | 0      |
    When I toggle the sidebar on
    And I kill the main pane of window "0"
    And I clean up orphan sidebars
    Then the sidebar count should be 0

  Scenario: No orphan sidebar is left in a window whose main pane is gone
    Given an isolated tmux server "tmuxrd-godog-b" with sessions:
      | session |
      | s1      |
    And session "s1" has windows:
      | window |
      | 0      |
      | 1      |
    When I toggle the sidebar on
    And I kill the main pane of window "0"
    And I clean up orphan sidebars
    Then window "0" should have no sidebar pane
    And window "1" should have exactly one sidebar pane
