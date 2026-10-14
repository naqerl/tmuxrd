@daemon @state
Feature: The daemon serves live agent state to sidebar clients
  A single tmuxrd daemon polls the server, detects each agent pane's state,
  and pushes it to every connected sidebar client over the Unix socket.

  Scenario: The daemon starts once and refuses a second instance
    Given an isolated tmux server "tmuxrd-godog-c" with sessions:
      | session |
      | s1      |
    And session "s1" has windows:
      | window |
      | 0      |
    When I start the daemon
    And I start the daemon again
    Then exactly one daemon should be running

  Scenario: Summary reports every agent pane with its state
    Given an isolated tmux server "tmuxrd-godog-c" with sessions:
      | session |
      | s1      |
      | s2      |
    And session "s1" has window "0" running agent "pi"
    And session "s2" has window "0" running agent "claude"
    And session "s2" has window "1" running agent "codex"
    When I run summary
    Then summary agents should be 3

  Scenario: Summary is zero when no agents are running
    Given an isolated tmux server "tmuxrd-godog-c" with sessions:
      | session |
      | s1      |
    And session "s1" has windows:
      | window |
      | 0      |
    When I run summary
    Then summary agents should be 0

  Scenario: Detector groups results by session for the sidebar
    Given an isolated tmux server "tmuxrd-godog-c" with sessions:
      | session |
      | alpha   |
      | beta    |
    And session "alpha" has window "0" running agent "pi"
    And session "beta" has window "0" running agent "claude"
    When I detect agent panes
    Then detection should find 2 panes
    And session "alpha" should have 1 detected pane
    And session "beta" should have 1 detected pane

  Scenario: A pane running a plain shell is not reported as an agent
    Given an isolated tmux server "tmuxrd-godog-c" with sessions:
      | session |
      | s1      |
    And session "s1" has windows:
      | window |
      | 0      |
    When I detect agent panes
    Then detection should find 0 panes
