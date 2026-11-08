package main

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/TheNerd707/HydroDamGame/internal"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

type tickMsg time.Time

type stringMsg string

type offsidePowerUpdate string

const (
	offsidePowerShutdown  offsidePowerUpdate = "shutdown"
	offsidePowerRecovered offsidePowerUpdate = "recovered"
)

type Model struct {
	input                 textarea.Model
	lastCommand           string
	screen                string
	now                   int
	nowCount              int
	gameState             GameState
	height                int
	width                 int
	pastCommands          []string
	eventLog              []string
	events                chan string
	offsidePowerUpdates   chan offsidePowerUpdate
	asciiLoop             int
	eventCount            int
	pauseGrid             bool
	lastOffsidePowerEvent time.Time
	gridMatchTicks        int
}

type reservoir struct {
	depth     float64
	inflow    float64
	outflow   float64
	tailwater *tailwater
	maxVolume float64
	maxDepth  float64
}

func (d Dam) calcTotalFLow() float64 {
	tflow := 0.0
	for _, turbine := range d.turbines {
		turbine.flowRate = internal.CalcTurbineFlow(turbine.wicketGate.angle, 90.0, 150, d.reservoir.depth-d.reservoir.tailwater.height, 120)
		tflow += turbine.flowRate
	}

	return d.reservoir.inflow - tflow - d.reservoir.outflow
}

func (d *Dam) reservoirUpdate(dt float64) {
	r := d.reservoir

	r.depth += internal.DepthChangeRateMs(r.maxVolume, r.maxDepth, r.depth, d.calcTotalFLow()) * dt

	r.depth = math.Max(0, math.Min(r.depth, r.maxDepth))
}

type turbine struct {
	unitNumber       int
	status           string
	synced           bool
	flowRate         float64
	rpm              float64
	turbineEfficency float64
	netHeadLoss      float64
	wicketGate       *wicketGate
}

type tailwater struct {
	flowRate float64
	height   float64
}

type generator struct {
	turbine    *turbine
	efficiency float64
}

type wicketGate struct {
	angle float64
}

type grid struct {
	online            bool
	offSitePowerInput float64
	demand            float64
	frequency         float64
}

type hydrolicPumps struct {
	unitNumber  int
	status      string
	temperature float64
}

type oilPump struct {
	unitNumber  int
	status      string
	pumpType    string
	temperature float64
}

type coolingPump struct {
	status      string
	flowRate    float64
	temperature float64
}

type Dam struct {
	name          string
	reservoir     *reservoir
	turbines      []*turbine
	generators    []*generator
	oilPumps      []*oilPump
	coolingPump   *coolingPump
	hydrolicPumps []*hydrolicPumps
	grid          *grid
}

type GameState struct {
	dam    *Dam
	points int
}

func newModel(gameState GameState) Model {
	input := textarea.New()
	input.Prompt = "> "
	input.ShowLineNumbers = false
	input.SetHeight(1)
	input.Focus()
	input.SetWidth(70)

	return Model{
		input:               input,
		lastCommand:         "None yet",
		now:                 0,
		nowCount:            0,
		screen:              "main",
		width:               200,
		gameState:           gameState,
		pastCommands:        []string{},
		eventLog:            []string{""},
		events:              make(chan string, 16),
		offsidePowerUpdates: make(chan offsidePowerUpdate, 2),
	}
}

func tick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(now time.Time) tea.Msg {
		return tickMsg(now)
	})
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.input.Focus(), tick())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyEnter:
			command := m.input.Value()
			trimmed := strings.TrimSpace(strings.ToLower(command))
			m.input.Reset()
			CommandHandler(&m, trimmed)

			m.lastCommand = command
			return m, m.input.Focus()
		}

		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	case tickMsg:
		if rand.Float64() < 0.01 {
			m.eventCount++
		}
		if m.eventCount > 25 && (m.lastOffsidePowerEvent.IsZero() || time.Since(m.lastOffsidePowerEvent) >= 5*time.Minute) {
			internal.OffsidePowerEvent(m.events)
			m.eventCount = -1
			m.pauseGrid = true
			m.lastOffsidePowerEvent = time.Now()

			go func() {
				time.Sleep(60 * time.Second)
				m.offsidePowerUpdates <- offsidePowerShutdown

				time.Sleep(30 * time.Second)
				m.offsidePowerUpdates <- offsidePowerRecovered
			}()

		}

		m.nowCount++
		m.asciiLoop++
		if m.nowCount%10 == 0 {
			m.now++
		}
		if m.nowCount%(60*10) == 0 {
			text := fmt.Sprintf("LOG: dam level at: %03.0fm", m.gameState.dam.reservoir.depth)
			m.addEventLog(text)
			if !m.pauseGrid {
				newDemand := 50 + rand.Float64()*150
				m.gameState.dam.grid.demand = newDemand
				internal.GridUpdateEvent(m.events, newDemand)
			}
		}
		m.gameState.dam.reservoirUpdate(0.1)
		select {
		case update := <-m.offsidePowerUpdates:
			switch update {
			case offsidePowerShutdown:
				m.gameState.dam.grid.online = false
				m.gameState.dam.grid.demand = 0

				successfulShutdown := true
				for _, turbine := range m.gameState.dam.turbines {
					if turbine.status != "Offline" {
						successfulShutdown = false
						m.events <- fmt.Sprintf("Turbine %d was not shutdown. 5 points lost", turbine.unitNumber)
						m.gameState.points -= 5
					}
				}
				if successfulShutdown {
					m.gameState.points += 20
					m.events <- "All turbines shutdown successfully. 20 points awarded."
				}
			case offsidePowerRecovered:
				m.gameState.dam.grid.online = true
				m.pauseGrid = false
				m.events <- "INFO: Offside power trip resolved. Grid is back online."
				m.lastOffsidePowerEvent = time.Now()
				m.gameState.dam.grid.demand = 50 + rand.Float64()*150
			}
		default:
		}
		totalPower := m.gameState.dam.totalPower()
		demand := m.gameState.dam.grid.demand
		if m.gameState.dam.grid.online && demand > 0 && math.Abs(totalPower-demand) <= demand*0.05 {
			m.gridMatchTicks++
			if m.gridMatchTicks >= 100 {
				m.gameState.points++
				m.gridMatchTicks = 0
			}
		} else {
			m.gridMatchTicks = 0
		}
		for {
			select {
			case event := <-m.events:
				m.addEventLog(event)
			default:
				return m, tick()
			}
		}

	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.width = msg.Width
		m.setUnusedCommandHeight()
	case stringMsg:
		m.addEventLog(string(msg))
	}

	return m, nil
}

func (d Dam) totalPower() float64 {
	totalPower := 0.0
	for _, generator := range d.generators {
		power := 1000 * 9.81 * generator.turbine.flowRate * generator.efficiency * generator.turbine.turbineEfficency * (d.reservoir.depth - d.reservoir.tailwater.height - d.turbines[generator.turbine.unitNumber-1].netHeadLoss) / 1e6
		if !generator.turbine.synced {
			power = 0
		}
		totalPower += power
	}
	return totalPower
}

func formatFlow(flow float64) string {
	if flow >= 100 || flow <= -100 {
		return fmt.Sprintf("%4.0f", flow)
	}
	return fmt.Sprintf("%04.1f", flow)
}

func (m Model) View() string {
	asciiArtArray := ascii(m.asciiLoop%3+1, m.width, m.gameState.dam.reservoir.depth)
	headerStatus := fmt.Sprintf("Day %02d  %02d:%02d  POINTS:%2d", m.now/60/24+1, m.now/60%24, m.now%60, m.gameState.points)

	turbineStatus := ""
	for i, turbine := range m.gameState.dam.turbines {
		turbineStatus += fmt.Sprintf(
			"║   %-17s%07s%18.1fm³/s%17.1f  ║"+asciiArtArray[8+i],
			fmt.Sprintf("%02d", turbine.unitNumber),
			turbine.status,
			turbine.flowRate,
			turbine.rpm,
		)
	}

	generatorStatus := ""
	for i, generator := range m.gameState.dam.generators {
		turbineUnitNumber := "N/A"
		if generator.turbine != nil {
			turbineUnitNumber = fmt.Sprintf("%02d", generator.turbine.unitNumber)
		}
		power := 1000 * 9.81 * generator.turbine.flowRate * generator.efficiency * generator.turbine.turbineEfficency * (m.gameState.dam.reservoir.depth - m.gameState.dam.reservoir.tailwater.height - m.gameState.dam.turbines[generator.turbine.unitNumber-1].netHeadLoss) / 1e6
		if !generator.turbine.synced {
			power = 0
		}
		generatorStatus += fmt.Sprintf(
			"║   %-19s%s%24s%16.1fMW  ║"+asciiArtArray[14+i],
			fmt.Sprintf("%02d", generator.turbine.unitNumber),
			turbineUnitNumber,
			generator.turbine.status,
			power,
		)
	}

	past := ""

	for i, str := range m.pastCommands {
		past += str
		eventIndex := len(m.eventLog) - i - 1
		if eventIndex >= 0 && eventIndex < len(m.eventLog) {
			past += m.eventLog[eventIndex]
		}
		past += "\n"
	}

	main := fmt.Sprintf(
		"╔════════════════════════════════════════════════════════════════════╗"+asciiArtArray[0]+
			"║%-42s%23s  ║"+asciiArtArray[1]+
			"╠════════════════════════════════════════════════════════════════════╣"+asciiArtArray[2]+
			"║  RESERVOIR                                                         ║"+asciiArtArray[3]+
			"║  Depth: %03.0fm          Inflow: %sm³/s          Outflow: %sm³/s  ║"+asciiArtArray[4]+
			"╠════════════════════════════════════════════════════════════════════╣"+asciiArtArray[5]+
			"║  TURBINES                                                          ║"+asciiArtArray[6]+
			"║  Unit              Status              Flow Rate             RPM   ║"+asciiArtArray[7]+
			"%s"+
			"╠════════════════════════════════════════════════════════════════════╣"+asciiArtArray[11]+
			"║  GENERATORS                                                        ║"+asciiArtArray[12]+
			"║  Unit              Turbine              Status             Output  ║"+asciiArtArray[13]+
			"%s"+
			"║                                                                    ║"+asciiArtArray[17]+
			"║  TOTAL GENERATION: %6.1fMW                                        ║"+asciiArtArray[18]+
			"║  GRID DEMAND: %6.1fMW                                             ║"+asciiArtArray[19]+
			"╠════════════════════════════════════════════════════════════════════╣"+asciiArtArray[20]+
			"║   ACTIVE ALARMS: %01d                                                 ║"+asciiArtArray[21]+
			"╚════════════════════════════════════════════════════════════════════╝"+asciiArtArray[22]+
			"%s",
		"  Burkenrich Hydroelectric Power Plant",
		headerStatus,
		m.gameState.dam.reservoir.depth,
		formatFlow(m.gameState.dam.reservoir.inflow),
		formatFlow(math.Abs(m.gameState.dam.calcTotalFLow()-m.gameState.dam.reservoir.inflow)),
		turbineStatus,
		generatorStatus,
		m.gameState.dam.totalPower(),
		m.gameState.dam.grid.demand,
		0,
		past,
	)

	return main + m.input.View()
}

func (m *Model) setUnusedCommandHeight() {
	heightLeft := m.height - 25

	length := len(m.pastCommands)
	legth2 := len(m.eventLog)

	if length < heightLeft {
		for range heightLeft - length {
			text := fmt.Sprintf("%-70s", "")
			m.pastCommands = append(m.pastCommands, text)
		}
	}

	if legth2 < heightLeft {
		for range heightLeft - legth2 {
			m.eventLog = append(m.eventLog, "")
		}
	}

}

func wrapText(input string, width int) []string {
	words := strings.Fields(input)
	var lines []string
	var current strings.Builder

	for _, word := range words {
		if current.Len() == 0 {
			current.WriteString(word)
		} else if current.Len()+1+len(word) <= width {
			current.WriteString(" ")
			current.WriteString(word)
		} else {
			lines = append(lines, current.String())
			current.Reset()
			current.WriteString(word)
		}
	}

	if current.Len() > 0 {
		lines = append(lines, current.String())
	}

	return lines
}

func (m *Model) addEventLog(event string) {
	for i := range len(m.eventLog) - 1 {
		if i == 0 {
			continue
		}

		m.eventLog[i] = m.eventLog[i+1]
	}
	event = fmt.Sprintf("[%02d:%02d] %s", m.now/60%24, m.now%60, event)
	if len(event) < 90 {
		text := fmt.Sprintf("%-90s", event)
		m.eventLog[len(m.eventLog)-1] = text
	} else {
		lines := wrapText(event, 90)
		text := fmt.Sprintf("%-90s", lines[0])
		m.eventLog[len(m.eventLog)-1] = text
		lines = lines[1:]
		rest := strings.Join(lines, "")
		m.addEventLog(rest)
	}
}

func (m *Model) commandOutput(out string) {
	for i := range len(m.pastCommands) - 1 {
		if i == 0 {
			continue
		}

		m.pastCommands[i] = m.pastCommands[i+1]
	}

	if len(out) < 70 {
		text := fmt.Sprintf("%-70s", out)
		m.pastCommands[len(m.pastCommands)-1] = text
	} else {
		lines := wrapText(out, 70)
		text := fmt.Sprintf("%-70s", lines[0])
		m.pastCommands[len(m.pastCommands)-1] = text
		lines = lines[1:]
		rest := strings.Join(lines, "")
		m.commandOutput(rest)
	}

}

func main() {
	gameState := GameState{
		dam: &Dam{
			name: "Burkenrich Hydroelectric Power Plant",
			reservoir: &reservoir{
				depth:   80.0,
				inflow:  0,
				outflow: 90,
				tailwater: &tailwater{
					flowRate: 45.2,
					height:   10,
				},
				maxVolume: 0.01,
				maxDepth:  120,
			},
			turbines: []*turbine{
				{
					unitNumber:       1,
					status:           "Offline",
					synced:           false,
					flowRate:         150.0,
					rpm:              0,
					turbineEfficency: 0.9,
					netHeadLoss:      5.0,
					wicketGate: &wicketGate{
						angle: 0,
					},
				},
				{
					unitNumber:       2,
					status:           "Offline",
					synced:           false,
					flowRate:         150.0,
					rpm:              0,
					turbineEfficency: 0.9,
					netHeadLoss:      5.0,
					wicketGate: &wicketGate{
						angle: 0,
					},
				},
				{
					unitNumber:       3,
					status:           "Offline",
					synced:           false,
					flowRate:         150.0,
					rpm:              0,
					turbineEfficency: 0.9,
					netHeadLoss:      5.0,
					wicketGate: &wicketGate{
						angle: 0,
					},
				},
			},
			generators: []*generator{
				{
					turbine:    nil, // Link to the corresponding turbine
					efficiency: 0.96,
				},
				{
					turbine:    nil,
					efficiency: 0.96,
				},
				{
					turbine:    nil,
					efficiency: 0.96,
				},
			},
			oilPumps: []*oilPump{
				{
					unitNumber:  1,
					status:      "Running",
					pumpType:    "Electric",
					temperature: 75.0,
				},
			},
			coolingPump: &coolingPump{
				status:      "Running",
				flowRate:    20.0,
				temperature: 60.0,
			},
			hydrolicPumps: []*hydrolicPumps{
				{
					unitNumber:  1,
					status:      "Running",
					temperature: 70.0,
				},
			},
			grid: &grid{
				online:            true,
				offSitePowerInput: 5.0,
				demand:            100.0,
				frequency:         60.0,
			},
		},
	}
	gameState.dam.generators[0].turbine = gameState.dam.turbines[0]
	gameState.dam.generators[1].turbine = gameState.dam.turbines[1]
	gameState.dam.generators[2].turbine = gameState.dam.turbines[2]

	program := tea.NewProgram(newModel(gameState), tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		fmt.Println("Error running application:", err)
	}
}

func CommandHandler(m *Model, command string) {
	commands := strings.Split(command, " ")

	switch commands[0] {
	case "start":
		if len(commands) <= 1 {
			m.commandOutput("ERR: Start takes multiple arguments: start turbine unitId")
			return
		}
		switch commands[1] {
		case "turbine":
			if len(commands) <= 2 {
				m.commandOutput("ERR: Start takes multiple arguments: start turbine unitId")
				return
			}
			num64, err := strconv.ParseInt(commands[2], 16, 64)

			if err != nil {
				m.commandOutput("ERR: Cannot start a turbine without unit id nomber")
				return
			}
			if int(num64) > len(m.gameState.dam.turbines) {
				m.commandOutput("ERR: Invalid turbine id")
				return
			}

			turbine := m.gameState.dam.turbines[num64-1]
			if turbine == nil {
				m.commandOutput("ERR: Invalid turbine id.")
			}
			if turbine.synced || turbine.status == "SYNCING" {
				m.commandOutput("ERR: Turbine is already online, did you mean 'stop'?")
				return
			}
			turbine.status = "SYNCING"
			turbine.rpm = 0
			turbine.wicketGate.angle = 15
			text := fmt.Sprintf("SUCCESS: Turbine %d is syncronizing.", turbine.unitNumber)
			m.commandOutput(text)
			m.events <- fmt.Sprintf("Turbine %d synchronization started.", turbine.unitNumber)
			go func() {
				ticker := time.NewTicker(100 * time.Millisecond)
				defer ticker.Stop()
				for range ticker.C {
					turbine.rpm++

					if turbine.rpm >= 150 {
						turbine.status = "Running"
						turbine.synced = true
						m.events <- fmt.Sprintf("Turbine %d synchronized and is running.", turbine.unitNumber)
						return
					}
				}
			}()
		default:
			m.commandOutput("ERR: Start currently can only be used for turbines, contact ${SYSTEMADMIN} for more information")
		}
	case "wicket":
		if len(commands) <= 2 {
			m.commandOutput("ERR: Wickets takes multiple arguments: wickets unitId angle")
			return
		}
		num64, err := strconv.ParseInt(commands[1], 16, 64)
		if err != nil {
			m.commandOutput("ERR: Invalid turbine id.")
			return
		}
		angle, err := strconv.ParseFloat(commands[2], 64)
		if err != nil {
			m.commandOutput("ERR: Invalid angle.")
			return
		}
		if int(num64) > len(m.gameState.dam.turbines) {
			m.commandOutput("ERR: Invalid turbine id")
			return
		}
		turbine := m.gameState.dam.turbines[num64-1]
		if turbine == nil {
			m.commandOutput("ERR: Invalid turbine id.")
			return
		}
		if turbine.status != "Running" {
			m.commandOutput("ERR: Turbine is not running, cannot set wicket angle.")
			return
		}
		if angle < 0 || angle > 90 {
			m.commandOutput("ERR: Invalid angle. Must be between 0 and 90 degrees.")
			return
		}

		go func() {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for range ticker.C {
				if turbine.wicketGate.angle < angle {
					turbine.wicketGate.angle += 0.5
				} else if turbine.wicketGate.angle > angle {
					turbine.wicketGate.angle -= 0.5
				} else {
					m.events <- fmt.Sprintf("Turbine %d wicket gate reached %.1f degrees.", turbine.unitNumber, angle)
					return
				}
			}
		}()
		m.commandOutput(fmt.Sprintf("SUCCESS: Turbine %d wicket set to %f.", turbine.unitNumber, angle))

	case "stop":
		if len(commands) <= 1 {
			m.commandOutput("ERR: Stop takes multiple arguments: stop turbine unitId")
			return
		}
		switch commands[1] {
		case "turbine":
			if len(commands) <= 2 {
				m.commandOutput("ERR: Stop takes multiple arguments: stop turbine unitId")
				return
			}
			num64, err := strconv.ParseInt(commands[2], 16, 64)

			if err != nil {
				m.commandOutput("ERR: Cannot stop a turbine without unit id nomber")
				return
			}
			if int(num64) > len(m.gameState.dam.turbines) {
				m.commandOutput("ERR: Invalid turbine id")
				return
			}

			turbine := m.gameState.dam.turbines[num64-1]
			if turbine == nil {
				m.commandOutput("ERR: Invalid turbine id.")
				return
			}
			if !turbine.synced || turbine.status == "RUNDOWN" {
				m.commandOutput("ERR: Turbine is already offline, did you mean 'start'?")
				return
			}
			turbine.status = "RUNDOWN"
			m.commandOutput(fmt.Sprintf("SUCCESS: Turbine %d is stopping.", turbine.unitNumber))
			m.events <- fmt.Sprintf("Turbine %d stopping.", turbine.unitNumber)
			go func() {
				ticker := time.NewTicker(100 * time.Millisecond)
				defer ticker.Stop()
				for range ticker.C {
					turbine.rpm--
					turbine.wicketGate.angle -= 0.5
					if turbine.wicketGate.angle < 0 {
						turbine.wicketGate.angle = 0
					}

					if turbine.rpm <= 0 {
						turbine.status = "Offline"
						turbine.synced = false
						turbine.wicketGate.angle = 0
						m.events <- fmt.Sprintf("Turbine %d stopped and is offline.", turbine.unitNumber)
						return
					}
				}
			}()
		default:
			m.commandOutput("ERR: Stop currently can only be used for turbines, contact ${SYSTEMADMIN} for more information")
		}
	case "help":
		m.commandOutput("Available commands: start, stop, wicket, help. Start takes arguments: start turbine unitId. Wicket takes arguments: wicket unitId angle.")
	default:
		text := fmt.Sprintf("Command %s is unreconized. Type 'help' for help", command)
		m.commandOutput(text)
	}
}

func ascii(spin int, width int, depth float64) [23]string {
	width = width - 70 - 43
	var array [23]string

	spaces := strings.Repeat(" ", max(0, width))

	// Clamp depth between 0 and 120
	depth = math.Max(0, math.Min(120, depth))

	// 17 rows of dam drawing; 0 = bottom, 16 = top
	waterRow := 16 - int((depth/120)*16)

	// Your existing spin1, spin2, spin3 definitions go here
	spin1 := spaces + "                /────────┐\n" + spaces +
		"               /        /│\n" + spaces +
		"              /       / /│\n" + spaces +
		"             /      / /  │\n" + spaces +
		"            /     / /    │\n" + spaces +
		"           /    / /      │\n" + spaces +
		"          /   / /        │\n" + spaces +
		"         /  / /          │\n" + spaces +
		"        / / /            │\n" + spaces +
		"       // /              │\n" + spaces +
		"      / /                │\n" + spaces +
		"     //  ┌───┐           │\n" + spaces +
		"    /    │ |/│           │\n" + spaces +
		"   /    /│/│ |===========│\n" + spaces +
		"  /    //└───┘           │\n" + spaces +
		" /=====/                 │\n" + spaces +
		"─────────────────────────┴\n"
	spin2 := spaces + "                /────────┐\n" + spaces +
		"               /        /│\n" + spaces +
		"              /       / /│\n" + spaces +
		"             /      / /  │\n" + spaces +
		"            /     / /    │\n" + spaces +
		"           /    / /      │\n" + spaces +
		"          /   / /        │\n" + spaces +
		"         /  / /          │\n" + spaces +
		"        / / /            │\n" + spaces +
		"       // /              │\n" + spaces +
		"      / /                │\n" + spaces +
		"     //  ┌───┐           │\n" + spaces +
		"    /    │\\/ │           │\n" + spaces +
		"   /    /│/\\ │===========│\n" + spaces +
		"  /    //└───┘           │\n" + spaces +
		" /=====/                 │\n" + spaces +
		"─────────────────────────┴\n"
	spin3 := spaces + "                /────────┐\n" + spaces +
		"               /        /│\n" + spaces +
		"              /       / /│\n" + spaces +
		"             /      / /  │\n" + spaces +
		"            /     / /    │\n" + spaces +
		"           /    / /      │\n" + spaces +
		"          /   / /        │\n" + spaces +
		"         /  / /          │\n" + spaces +
		"        / / /            │\n" + spaces +
		"       // /              │\n" + spaces +
		"      / /                │\n" + spaces +
		"     //  ┌───┐           │\n" + spaces +
		"    /    │\\| │           │\n" + spaces +
		"   /    /│ |\\│===========│\n" + spaces +
		"  /    //└───┘           │\n" + spaces +
		" /=====/                 │\n" + spaces +
		"─────────────────────────┴\n"

	text := spin1
	switch spin {
	case 1:
		text = spin1
	case 2:
		text = spin2
	default:
		text = spin3
	}

	temp := strings.Split(strings.TrimSuffix(text, "\n"), "\n")

	// Add water to the right side of the dam
	for i := range temp {
		if i == len(temp)-1 {
			temp[i] = temp[i] + strings.Repeat("─", 16)
			continue
		}
		if i >= waterRow {
			temp[i] = temp[i] + strings.Repeat("~", 16)
		} else {
			temp[i] = temp[i] + strings.Repeat(" ", 16)
		}
	}

	for i := range len(array) - len(temp) {
		array[i] = "\n"
	}

	for i := range len(temp) {
		// The drawing lines already include the horizontal offset. Adding it
		// again here pushed the ASCII art off the right side of the screen.
		array[i+len(array)-len(temp)] = temp[i] + "\n"
	}

	return array
}
