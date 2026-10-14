@daemon @client @reconnect
Feature: A sidebar client reattaches when the daemon restarts
  A sidebar client holds a long-lived connection to the daemon. If the daemon
  is restarted (an upgrade, a crash), the client must reconnect on its own.
  Otherwise it keeps painting its last snapshot, and because the working
  spinner animates locally the visible symptoms are a *frozen timer* and a
  selection marker that only moves on the 5s local tick — which reads as
  "the cursor is slow".

  Scenario: The timer keeps advancing across a daemon restart
    Given an isolated tmux server "tmuxrd-godog-rc1"
    And session "s1" is created with agent windows:
      | agent | state   |
      | pi    | working |
    And the tmuxrd config is loaded
    And the sidebar is enabled
    When I toggle the sidebar on
    And the sidebar should show a timer for agent "pi"
    When I restart the daemon
    Then the sidebar should show a timer for agent "pi"
    And the timer for agent "pi" should increase within 5000ms

  Scenario: The sidebar catches up to a state that changed while it was away
    Given an isolated tmux server "tmuxrd-godog-rc2"
    And session "s1" is created with agent windows:
      | agent | state   |
      | pi    | working |
    And the tmuxrd config is loaded
    And the sidebar is enabled
    When I toggle the sidebar on
    And the sidebar should show a timer for agent "pi"
    When I restart the daemon
    And agent "pi" in session "s1" window "0" is in state "blocked"
    Then the sidebar should show agent "pi" as "?"
