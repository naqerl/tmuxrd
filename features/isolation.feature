@isolation @regression
Feature: Test scenarios can never touch the live tmux server
  Every operation must go through the tmux.Server abstraction with an explicit
  -L socket. A scenario must fail loudly rather than fall back to the default
  (live) tmux server.

  Scenario: The env server addresses only its own socket
    Given an isolated tmux server "tmuxrd-godog-e" with sessions:
      | session |
      | s1      |
    Then the server should be the isolated one
    And the server should never target the default socket

  Scenario: Killing the env server does not affect other sockets
    Given an isolated tmux server "tmuxrd-godog-e" with sessions:
      | session |
      | s1      |
    And session "s1" has windows:
      | window |
      | 0      |
    When I kill the isolated server
    Then the isolated server should be gone

  Scenario: Loading the config cannot start a daemon on the default server
    Given an isolated tmux server "tmuxrd-godog-e" with sessions:
      | session |
      | s1      |
    And session "s1" has windows:
      | window |
      | 0      |
    When the tmuxrd config is loaded
    Then the isolated server should advertise its own socket
    And exactly one daemon should be running
