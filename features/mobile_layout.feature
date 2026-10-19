@mobile @layout
Feature: The sidebar adapts its shape to the window width
  A tmux window's layout is shared by every attached client, so a phone and a
  desktop cannot each have their own. What tmuxrd does is follow the width of
  the window: a 32-column left sidebar on a desktop, and a full-width 2-row top
  strip on a phone (below @tmuxrd_mobile_width, default 60) where columns are
  scarce and rows are not. The shape is re-derived whenever the window resizes.

  Scenario: A narrow window gets a full-width top strip
    Given an isolated tmux server "tmuxrd-godog-ml1"
    And session "s1" is created with agent windows:
      | agent |
      | pi    |
    When I resize window "0" in session "s1" to 40x24
    And I toggle the sidebar on
    Then window "0" of session "s1" should have a strip sidebar
    And the sidebar of window "0" in session "s1" should be 2 rows tall
    And the sidebar of window "0" in session "s1" should span the window width

  Scenario: Widening the window turns the strip back into a left sidebar
    Given an isolated tmux server "tmuxrd-godog-ml2"
    And session "s1" is created with agent windows:
      | agent |
      | pi    |
    When I resize window "0" in session "s1" to 40x24
    And I toggle the sidebar on
    Then window "0" of session "s1" should have a strip sidebar
    When I resize window "0" in session "s1" to 220x50
    And I adapt the sidebar layout
    Then window "0" of session "s1" should have a side sidebar
    And the sidebar of window "0" in session "s1" should be 32 columns wide

  Scenario: The daemon reshapes the sidebar when the window width changes
    # No explicit adapt: the daemon's tick is the backstop that catches a client
    # whose size changed without any config hook firing.
    Given an isolated tmux server "tmuxrd-godog-ml3"
    And session "s1" is created with agent windows:
      | agent |
      | pi    |
    When I resize window "0" in session "s1" to 40x24
    And I toggle the sidebar on
    Then window "0" of session "s1" should have a strip sidebar
    When I resize window "0" in session "s1" to 220x50
    And I wait 1500ms
    Then window "0" of session "s1" should have a side sidebar
    And the sidebar of window "0" in session "s1" should be 32 columns wide

  Scenario: Re-adapting at the same width never churns the pane
    Given an isolated tmux server "tmuxrd-godog-ml4"
    And session "s1" is created with agent windows:
      | agent |
      | pi    |
    When I resize window "0" in session "s1" to 40x24
    And I toggle the sidebar on
    And I remember the sidebar pane of window "0" in session "s1"
    And I adapt the sidebar layout
    And I adapt the sidebar layout
    Then window "0" of session "s1" should have a strip sidebar
    And the sidebar pane of window "0" in session "s1" should be unchanged

  Scenario: The strip renders exactly two rows: chips and a detail line
    Given an isolated tmux server "tmuxrd-godog-ml5"
    And session "s1" is created with agent windows:
      | agent  |
      | pi     |
      | claude |
    When I resize window "0" in session "s1" to 40x24
    And I toggle the sidebar on
    Then window "0" of session "s1" should have a strip sidebar
    And the sidebar of window "0" in session "s1" should show exactly 2 lines
    And the sidebar of window "0" in session "s1" should mention "pi"
    And the sidebar of window "0" in session "s1" should mention "claude"

  Scenario: A strip squeezed by a window resize is grown back to two rows
    # tmux shrinks the smallest pane first, so a 2-row strip becomes 1 row when
    # the window gets shorter, hiding the detail line. The layout pass restores
    # it on the next tick.
    Given an isolated tmux server "tmuxrd-godog-ml7"
    And session "s1" is created with agent windows:
      | agent | state   |
      | pi    | working |
    When I resize window "0" in session "s1" to 43x30
    And I toggle the sidebar on
    And the sidebar of window "0" in session "s1" should be 2 rows tall
    When I resize window "0" in session "s1" to 43x21
    And I wait 1500ms
    Then the sidebar of window "0" in session "s1" should be 2 rows tall
    And the sidebar of window "0" in session "s1" should show exactly 2 lines

  Scenario: A number key jumps straight to that agent from the strip
    Given an isolated tmux server "tmuxrd-godog-ml6"
    And session "s1" is created with agent windows:
      | agent  |
      | pi     |
      | claude |
    When I resize window "0" in session "s1" to 40x24
    And I toggle the sidebar on
    And the sidebar is showing agent rows
    And I select window "0"
    When I press the "2" key in the sidebar of window "0" in session "s1"
    Then window "1" should be the active window of session "s1"
    And window "1" should have its agent pane focused

  Scenario: Relative navigation keeps the keyboard on the sidebar
    # j/k must not hand the keyboard to the agent: the point is to keep
    # stepping. Direct selection (a tap or 1-9) is what focuses the agent.
    Given an isolated tmux server "tmuxrd-godog-ml8"
    And session "s1" is created with agent windows:
      | agent  |
      | pi     |
      | claude |
    And the tmuxrd config is loaded
    And the sidebar is enabled
    When I toggle the sidebar on
    And the sidebar is showing agent rows
    And I select window "0"
    Then the sidebar should select window "0" within 250ms
    When I press the "j" key in the sidebar
    Then the sidebar should select window "1" within 250ms
    And window "1" should be the active window of session "s1"
    And window "1" of session "s1" should have its sidebar pane focused

  Scenario: A widened side sidebar is put back to the configured width
    # tmux splits extra columns evenly, so growing a window widens a sidebar
    # that was split at 32. The daemon's layout pass puts the width back.
    Given an isolated tmux server "tmuxrd-godog-ml9"
    And session "s1" is created with agent windows:
      | agent |
      | pi    |
    When I resize window "0" in session "s1" to 100x30
    And I toggle the sidebar on
    Then window "0" of session "s1" should have a side sidebar
    And the sidebar of window "0" in session "s1" should be 32 columns wide
    When I resize window "0" in session "s1" to 220x50
    And I wait 1500ms
    Then window "0" of session "s1" should have a side sidebar
    And the sidebar of window "0" in session "s1" should be 32 columns wide
