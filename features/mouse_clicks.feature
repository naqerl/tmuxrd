@mouse @client
Feature: Tapping or clicking the sidebar selects an agent
  A touch client cannot press `1`-`9`, so a tap must work: a tap on a chip in
  the mobile strip, or on a row of the desktop sidebar, selects that agent's
  window. The wheel steps through agents. (tmux must have `mouse on` for it to
  forward the events; the app also has to ask for the mouse.)

  Scenario: A click on a desktop sidebar row selects that agent
    Given an isolated tmux server "tmuxrd-godog-mouse1"
    And session "s1" is created with agent windows:
      | agent  |
      | pi     |
      | claude |
    And the tmuxrd config is loaded
    And the sidebar is enabled
    When I toggle the sidebar on
    And the sidebar is showing agent rows
    And I select window "1"
    Then window "1" should be the active window of session "s1"
    When I click cell "3,3" in the sidebar of window "1" in session "s1"
    Then window "0" should be the active window of session "s1"
    And window "0" should have its agent pane focused

  Scenario: A tap on a strip chip selects that agent
    Given an isolated tmux server "tmuxrd-godog-mouse2"
    And session "s1" is created with agent windows:
      | agent  |
      | pi     |
      | claude |
    And the tmuxrd config is loaded
    And the sidebar is enabled
    When I resize window "0" in session "s1" to 50x24
    And I toggle the sidebar on
    Then window "0" of session "s1" should have a strip sidebar
    When I select window "1"
    Then window "1" should be the active window of session "s1"
    When I click cell "1,0" in the sidebar of window "0" in session "s1"
    Then window "0" should be the active window of session "s1"
    And window "0" should have its agent pane focused
