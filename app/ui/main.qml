// rm-onleihe frontend (AppLoad / Qt Quick). Designed for e-ink:
// black on white, no animations, large touch targets.
import QtQuick 2.15
import QtQuick.Controls 2.15
import QtQuick.Layouts 1.15
import net.asivery.AppLoad 1.0

Rectangle {
    id: root
    anchors.fill: parent
    color: "white"

    // Required by AppLoad.
    signal close
    function unloading() {
        // Keep the backend alive only while the app is open.
        backend.terminate()
    }

    // ---- protocol (must match backend/main.go) ----
    readonly property int reqInit: 1
    readonly property int reqSetHost: 2
    readonly property int reqLogin: 3
    readonly property int reqSearch: 4
    readonly property int reqLend: 5
    readonly property int reqLoans: 6
    readonly property int reqDownload: 7
    readonly property int reqLogout: 8
    readonly property int reqSettings: 9

    property var appState: ({ loggedIn: false })
    property string page: "search"        // search | loans | settings
    property string busyText: ""
    property string message: ""
    property var libraries: []
    property string pendingHost: ""
    property var selectedLibrary: null
    property string lastQuery: ""
    property int searchFrom: 0
    property int searchTotal: 0
    property var lastItems: []
    property string lastSearchMessage: ""

    function send(type, obj) { backend.sendMessage(type, JSON.stringify(obj || {})) }

    AppLoad {
        id: backend
        applicationID: "rm-onleihe"
        onMessageReceived: (type, contents) => {
            let d = {}
            try { d = contents ? JSON.parse(contents) : {} } catch (e) { return }
            switch (type) {
            case 101: {
                const hideChanged = !!root.appState.hideDrm !== !!d.hideDrm
                root.appState = d
                if (hideChanged && root.lastItems.length > 0) fillSearch()
                // Logged out or session expired: keep the saved library so only
                // the password has to be entered again.
                if (!d.loggedIn && d.libraryId && root.selectedLibrary === null) {
                    root.pendingHost = d.host || ""
                    root.selectedLibrary = { onleiheId: d.onleiheId, libraryId: d.libraryId,
                                             libraryName: d.libraryName || "" }
                }
                if (d.loggedIn) passField.text = ""
                break
            }
            case 102: root.message = "⚠ " + d.message; break
            case 103:
                root.lastItems = d.items || []
                root.searchFrom = d.from; root.searchTotal = d.total
                root.lastSearchMessage = d.total + " Treffer für „" + d.query + "“"
                fillSearch()
                break
            case 104:
                loansModel.clear()
                for (const it of d.items || []) loansModel.append(flatten(it))
                root.message = (d.items || []).length + " aktuelle Ausleihen"
                break
            case 105: root.message = (d.ok ? "✓ " : (d.drm ? "🔒 " : "⚠ ")) + d.message; break
            case 106: root.busyText = d.busy ? d.text : ""; break
            case 107:
                root.pendingHost = d.host
                root.libraries = d.items || []
                root.selectedLibrary = root.libraries.length === 1 ? root.libraries[0] : null
                if (root.libraries.length === 0) root.message = "⚠ Keine Bibliothek gefunden."
                break
            case 108: root.message = "Ausgeliehen. Download läuft …"; break
            }
        }
    }

    function flatten(it) {
        return {
            productId: it.productId || "", title: it.title || "(ohne Titel)",
            subtitle: it.subtitle || "", authors: it.authors || "",
            mediaType: it.mediaType || "", lendId: it.lendId || "",
            availability: it.availability || "", canLend: !!it.canLend,
            drm: it.drm || ""
        }
    }

    // Fills the result list from the last search, hiding titles known to
    // have Adobe DRM if that setting is on. Titles with unknown status stay.
    function fillSearch() {
        searchModel.clear()
        let hidden = 0
        for (const it of lastItems) {
            if (appState.hideDrm && it.drm === "drm") { hidden++; continue }
            searchModel.append(flatten(it))
        }
        root.message = lastSearchMessage + (hidden > 0 ? " (" + hidden + " mit DRM ausgeblendet)" : "")
    }

    function drmLabel(drm) {
        if (drm === "free") return "DRM-frei ✓ KOReader"
        if (drm === "drm") return "🔒 Adobe-DRM"
        return "DRM: unbekannt"
    }

    ListModel { id: searchModel }
    ListModel { id: loansModel }

    Component.onCompleted: send(reqInit)

    // ------------------------------------------------------------ widgets
    component EButton: Rectangle {
        id: btn
        property alias text: lbl.text
        property bool active: false
        signal clicked
        implicitWidth: Math.max(160, lbl.implicitWidth + 48)
        implicitHeight: 72
        color: active ? "black" : "white"
        border.color: "black"; border.width: 3; radius: 8
        opacity: enabled ? 1 : 0.4
        Text { id: lbl; anchors.centerIn: parent; font.pixelSize: 30; color: btn.active ? "white" : "black" }
        MouseArea { anchors.fill: parent; onClicked: btn.clicked() }
    }

    component EField: TextField {
        font.pixelSize: 32
        Layout.fillWidth: true
        implicitHeight: 80
        background: Rectangle { border.color: "black"; border.width: 2; radius: 6 }
    }

    // ------------------------------------------------------------- layout
    ColumnLayout {
        anchors.fill: parent
        anchors.margins: 32
        spacing: 20

        RowLayout {
            Layout.fillWidth: true
            spacing: 16
            Text { text: "Onleihe"; font.pixelSize: 48; font.bold: true }
            Text {
                text: appState.loggedIn ? (appState.libraryName || appState.host) : ""
                font.pixelSize: 26; elide: Text.ElideRight; Layout.fillWidth: true
            }
            EButton { text: "Schließen"; onClicked: root.close() }
        }

        RowLayout {
            visible: appState.loggedIn
            spacing: 16
            EButton { text: "Suche"; active: page === "search"; onClicked: page = "search" }
            EButton {
                text: "Meine Ausleihen"; active: page === "loans"
                onClicked: { page = "loans"; send(reqLoans) }
            }
            EButton { text: "Einstellungen"; active: page === "settings"; onClicked: page = "settings" }
        }

        Text {
            Layout.fillWidth: true
            visible: text !== ""
            text: busyText !== "" ? "⏳ " + busyText : message
            wrapMode: Text.Wrap; font.pixelSize: 26
        }

        Rectangle { Layout.fillWidth: true; height: 2; color: "black" }

        // ------------------------------------------------------- login
        ColumnLayout {
            visible: !appState.loggedIn
            Layout.fillWidth: true; Layout.fillHeight: true
            spacing: 18

            Text { text: "Anmelden"; font.pixelSize: 38; font.bold: true }
            Text {
                text: "Adresse deiner Onleihe (z. B. niedersachsen.onleihe.de oder www.onleihe.de/berlin):"
                font.pixelSize: 26; wrapMode: Text.Wrap; Layout.fillWidth: true
            }
            RowLayout {
                Layout.fillWidth: true
                EField { id: hostField; text: appState.host || ""; placeholderText: "xyz.onleihe.de" }
                EButton { text: "Bibliothek suchen"; onClicked: send(reqSetHost, { host: hostField.text }) }
            }

            ListView {
                id: libList
                visible: libraries.length > 1
                Layout.fillWidth: true
                Layout.preferredHeight: Math.min(libraries.length * 76, 460)
                clip: true
                model: libraries
                boundsBehavior: Flickable.StopAtBounds
                delegate: Rectangle {
                    width: libList.width; height: 76
                    property bool sel: selectedLibrary && selectedLibrary.libraryId === modelData.libraryId
                    color: sel ? "black" : "white"
                    border.color: "black"; border.width: 1
                    Text {
                        anchors.verticalCenter: parent.verticalCenter; x: 16
                        width: parent.width - 32; elide: Text.ElideRight
                        text: (modelData.libraryName || modelData.libraryId) + (modelData.city ? " (" + modelData.city + ")" : "")
                        font.pixelSize: 28; color: parent.sel ? "white" : "black"
                    }
                    MouseArea { anchors.fill: parent; onClicked: selectedLibrary = modelData }
                }
            }

            Text {
                visible: selectedLibrary !== null
                text: selectedLibrary ? "Bibliothek: " + (selectedLibrary.libraryName || selectedLibrary.libraryId) : ""
                font.pixelSize: 28
            }
            EField { id: userField; visible: selectedLibrary !== null; placeholderText: "Ausweisnummer / Benutzername"; text: appState.username || "" }
            EField { id: passField; visible: selectedLibrary !== null; placeholderText: "Passwort"; echoMode: TextInput.Password }
            EButton {
                visible: selectedLibrary !== null
                enabled: busyText === ""
                text: "Anmelden"
                onClicked: send(reqLogin, {
                    host: pendingHost,
                    onleiheId: selectedLibrary.onleiheId,
                    libraryId: selectedLibrary.libraryId,
                    libraryName: selectedLibrary.libraryName || "",
                    username: userField.text,
                    password: passField.text
                })
            }
            Text {
                text: "Das Passwort wird nicht gespeichert, nur das Sitzungs-Token."
                font.pixelSize: 22; color: "#444"
            }
            Item { Layout.fillHeight: true }
        }

        // ------------------------------------------------------- search
        ColumnLayout {
            visible: appState.loggedIn && page === "search"
            Layout.fillWidth: true; Layout.fillHeight: true
            spacing: 16

            RowLayout {
                Layout.fillWidth: true
                EField {
                    id: queryField; placeholderText: "Titel, Autor, Stichwort …"
                    onAccepted: doSearch(0)
                }
                EButton { text: "Suchen"; onClicked: doSearch(0) }
                EButton {
                    text: "DRM ausblenden"
                    active: !!appState.hideDrm
                    onClicked: send(reqSettings, { hideDrm: !appState.hideDrm })
                }
            }

            ListView {
                id: results
                Layout.fillWidth: true; Layout.fillHeight: true
                clip: true; spacing: 12
                boundsBehavior: Flickable.StopAtBounds
                model: searchModel
                delegate: mediaDelegate
                footer: RowLayout {
                    width: results.width
                    visible: searchTotal > 0
                    spacing: 16
                    EButton { text: "◀ Zurück"; enabled: searchFrom > 0; onClicked: doSearch(Math.max(0, searchFrom - 20)) }
                    Text {
                        text: (searchFrom + 1) + "–" + Math.min(searchFrom + 20, searchTotal) + " von " + searchTotal
                        font.pixelSize: 26; Layout.fillWidth: true; horizontalAlignment: Text.AlignHCenter
                    }
                    EButton { text: "Weiter ▶"; enabled: searchFrom + 20 < searchTotal; onClicked: doSearch(searchFrom + 20) }
                }
            }
        }

        // -------------------------------------------------------- loans
        ListView {
            id: loans
            visible: appState.loggedIn && page === "loans"
            Layout.fillWidth: true; Layout.fillHeight: true
            clip: true; spacing: 12
            boundsBehavior: Flickable.StopAtBounds
            model: loansModel
            delegate: mediaDelegate
        }

        // ----------------------------------------------------- settings
        ColumnLayout {
            visible: appState.loggedIn && page === "settings"
            Layout.fillWidth: true; Layout.fillHeight: true
            spacing: 18

            Text { text: "Download-Ordner (in KOReader öffnen):"; font.pixelSize: 28 }
            EField { id: dirField; text: appState.downloadDir || "" }
            Text { text: "Leihfrist in Tagen (0 = Standard der Bibliothek):"; font.pixelSize: 28 }
            EField { id: daysField; text: String(appState.lendDays || 0); inputMethodHints: Qt.ImhDigitsOnly }
            EButton {
                text: "Speichern"
                onClicked: send(reqSettings, { downloadDir: dirField.text, lendDays: parseInt(daysField.text) || 0 })
            }
            Item { height: 24 }
            Text { text: "Angemeldet als " + (appState.username || "?"); font.pixelSize: 26 }
            EButton { text: "Abmelden"; onClicked: send(reqLogout) }
            Text { text: "rm-onleihe " + (appState.version || ""); font.pixelSize: 22; color: "#444" }
            Item { Layout.fillHeight: true }
        }
    }

    function doSearch(from) {
        if (queryField.text.trim() === "") return
        lastQuery = queryField.text
        send(reqSearch, { query: lastQuery, from: from })
    }

    Component {
        id: mediaDelegate
        Rectangle {
            width: ListView.view.width
            height: col.implicitHeight + 32
            border.color: "black"; border.width: 2; radius: 8
            RowLayout {
                anchors.fill: parent; anchors.margins: 16
                spacing: 16
                ColumnLayout {
                    id: col
                    Layout.fillWidth: true
                    spacing: 4
                    Text { text: model.title; font.pixelSize: 30; font.bold: true; wrapMode: Text.Wrap; Layout.fillWidth: true }
                    Text { visible: model.subtitle !== ""; text: model.subtitle; font.pixelSize: 24; wrapMode: Text.Wrap; Layout.fillWidth: true }
                    Text { text: model.authors; font.pixelSize: 26; Layout.fillWidth: true; elide: Text.ElideRight }
                    Text {
                        text: [model.mediaType, drmLabel(model.drm), model.availability].filter(s => s).join(" · ")
                        font.pixelSize: 22; color: "#444"; wrapMode: Text.Wrap; Layout.fillWidth: true
                    }
                }
                EButton {
                    visible: model.lendId === ""
                    enabled: busyText === ""
                    text: "Ausleihen"
                    onClicked: send(reqLend, { productId: model.productId, title: model.title, authors: model.authors })
                }
                EButton {
                    visible: model.lendId !== ""
                    enabled: busyText === ""
                    text: "Download"
                    onClicked: send(reqDownload, { lendId: model.lendId, title: model.title, authors: model.authors })
                }
            }
        }
    }
}
