local LrDialogs = import "LrDialogs"
local LrPathUtils = import "LrPathUtils"
local LrTasks = import "LrTasks"
local LrView = import "LrView"

local API = require "CuratorAPI"
local Keywords = require "CuratorKeywords"

local bind = LrView.bind
local share = LrView.share

local provider = {
    supportsIncrementalPublish = true,
    supportsCustomSortOrder = true,
    canExportVideo = false,
    small_icon = LrPathUtils.child("assets", "Curator.png"),

    exportPresetFields = {
        { key = "curatorURL", default = "http://127.0.0.1:8080" },
        { key = "curatorToken", default = "" },
        { key = "curatorSourceID", default = "" },
    },

    allowFileFormats = { "JPEG" },
    allowColorSpaces = { "sRGB" },
    hideSections = { "exportLocation", "fileNaming", "video" },
}

function provider.startDialog(propertyTable)
    if propertyTable.curatorURL == "" and propertyTable.LR_curatorURL then
        propertyTable.curatorURL = propertyTable.LR_curatorURL
    end
    if propertyTable.curatorToken == "" and propertyTable.LR_curatorToken then
        propertyTable.curatorToken = propertyTable.LR_curatorToken
    end
    if propertyTable.curatorSourceID == "" then
        propertyTable.curatorSourceID = string.format("%x-%x", os.time(), math.random(0, 0x7fffff))
    end
    local existingToken = API.token(propertyTable)
    if existingToken ~= "" then
        API.storeToken(propertyTable, existingToken)
    end
    propertyTable.curatorToken = ""
    propertyTable.curatorTokenInput = ""
    propertyTable.curatorTokenConfigured = existingToken ~= ""
    local function validate()
        local missing = propertyTable.curatorURL == ""
            or (not propertyTable.curatorTokenConfigured and propertyTable.curatorTokenInput == "")
        propertyTable.LR_cantExportBecause = missing and "Curator URL and token are required" or nil
    end
    propertyTable:addObserver("curatorURL", validate)
    propertyTable:addObserver("curatorTokenInput", validate)
    validate()
end

function provider.endDialog(propertyTable, why)
    if why == "ok" and propertyTable.curatorTokenInput and propertyTable.curatorTokenInput ~= "" then
        local stored, err = API.storeToken(propertyTable, propertyTable.curatorTokenInput)
        if not stored then error(tostring(err)) end
        propertyTable.curatorTokenConfigured = true
    end
    propertyTable.curatorToken = ""
    propertyTable.curatorTokenInput = ""
end

function provider.sectionsForTopOfDialog(viewFactory, propertyTable)
    return {
        {
            title = "Curator",
            synopsis = bind("curatorURL"),
            viewFactory:column {
                bind_to_object = propertyTable,
                spacing = viewFactory:control_spacing(),
                viewFactory:row {
                    viewFactory:static_text { title = "Server URL", width = share("label_width") },
                    viewFactory:edit_field { value = bind("curatorURL"), width_in_chars = 36 },
                },
                viewFactory:row {
                    viewFactory:static_text { title = "Publishing token", width = share("label_width") },
                    viewFactory:password_field { value = bind("curatorTokenInput"), width_in_chars = 36 },
                },
                viewFactory:row {
                    viewFactory:static_text { title = "", width = share("label_width") },
                    viewFactory:push_button {
                        title = "Test connection",
                        action = function()
                            LrTasks.startAsyncTask(function()
                                local result, err = API.capabilities(propertyTable)
                                if result then
                                    LrDialogs.message("Curator", "Connected to " .. result.name .. " v" .. tostring(result.version), "info")
                                else
                                    LrDialogs.message("Curator connection failed", tostring(err), "critical")
                                end
                            end)
                        end,
                    },
                },
            },
        },
    }
end

local function externalKey(settings, localID)
    return tostring(settings.curatorSourceID) .. ":" .. tostring(localID)
end

local function collectionParents(collection)
    local parents = {}
    local parent = collection:getParent()
    while parent do
        table.insert(parents, 1, parent)
        parent = parent:getParent()
    end
    return parents
end

local function recordRemoteIDs(catalog, assignments)
    if #assignments == 0 then return end
    catalog:withWriteAccessDo("Record Curator collection IDs", function()
        for _, assignment in ipairs(assignments) do
            assignment.collection:setRemoteId(tostring(assignment.remoteID))
        end
    end)
end

local function syncHierarchy(settings, collection, name)
    local parentExternalID = ""
    local assignments = {}
    for _, parent in ipairs(collectionParents(collection)) do
        local parentID = externalKey(settings, parent.localIdentifier)
        local result, err = API.syncGallery(settings, parentID, parent:getName(), parentExternalID)
        if not result then return nil, err end
        table.insert(assignments, { collection = parent, remoteID = result.id })
        parentExternalID = parentID
    end
    local externalID = externalKey(settings, collection.localIdentifier)
    local result, err = API.syncGallery(settings, externalID, name, parentExternalID)
    return result, err, externalID, assignments
end

local function syncPublishedCollection(settings, exportContext)
    local info = exportContext.publishedCollectionInfo or {}
    local collection = exportContext.publishedCollection
    if not collection then return nil, "This export is not associated with a published collection" end
    local result, err, externalID, assignments = syncHierarchy(settings, collection, info.name)
    if not result then return nil, err end
    recordRemoteIDs(exportContext.exportSession.catalog, assignments)
    exportContext.exportSession:recordRemoteCollectionId(tostring(result.id))
    if result.url and result.url ~= "" then
        exportContext.exportSession:recordRemoteCollectionUrl(result.url)
    end
    return result.id, externalID, nil
end

local function syncCollectionInfo(settings, info)
    local collection = info.publishedCollection
    if not collection then error("Curator could not identify the Lightroom collection") end
    local result, err, _, assignments = syncHierarchy(settings, collection, info.name)
    if not result then error(tostring(err)) end
    table.insert(assignments, { collection = collection, remoteID = result.id })
    recordRemoteIDs(collection.catalog, assignments)
end

local function collectionSettingsView(viewFactory)
    return viewFactory:group_box {
        title = "Curator",
        viewFactory:static_text { title = "This collection is synchronized with Curator." },
    }
end

function provider.viewForCollectionSettings(viewFactory)
    return collectionSettingsView(viewFactory)
end

function provider.viewForCollectionSetSettings(viewFactory)
    return collectionSettingsView(viewFactory)
end

local function updateCollection(settings, info)
    syncCollectionInfo(settings, info)
    local _, err = API.build(settings)
    if err then error(tostring(err)) end
end

provider.updateCollectionSettings = updateCollection
provider.updateCollectionSetSettings = updateCollection

function provider.metadataThatTriggersRepublish()
    return { default = false, caption = true, keywords = true }
end

function provider.shouldDeletePublishService()
    return "delete"
end

function provider.willDeletePublishService(settings)
    API.storeToken(settings, "")
end

function provider.renamePublishedCollection(settings, info)
    syncCollectionInfo(settings, info)
    local _, err = API.build(settings)
    if err then error(tostring(err)) end
end

function provider.reparentPublishedCollection(settings, info)
    syncCollectionInfo(settings, info)
    local _, err = API.build(settings)
    if err then error(tostring(err)) end
end

function provider.deletePublishedCollection(settings, info)
    if info.remoteId == nil or tostring(info.remoteId) == "" then return end
    local _, err = API.deleteGallery(settings, info.remoteId)
    if err then error(tostring(err)) end
    _, err = API.build(settings)
    if err then error(tostring(err)) end
end

function provider.deletePhotosFromPublishedCollection(settings, photoIDs, deletedCallback)
    for _, photoID in ipairs(photoIDs) do
        local _, err = API.deletePhoto(settings, photoID)
        if err then error(tostring(err)) end
        deletedCallback(photoID)
    end
    local _, err = API.build(settings)
    if err then error(tostring(err)) end
end

function provider.imposeSortOrderOnPublishedCollection(settings, info, remoteIDSequence)
    local galleryID = info.remoteCollectionId or info.remoteId
    if galleryID == nil then error("Curator gallery has not been synchronized") end
    local _, err = API.setPhotoOrder(settings, galleryID, remoteIDSequence)
    if err then error(tostring(err)) end
    _, err = API.build(settings)
    if err then error(tostring(err)) end
end

function provider.processRenderedPhotos(_, exportContext)
    local settings = exportContext.propertyTable
    local progress = exportContext:configureProgress { title = "Publishing to Curator", renderPortion = 0.5 }
    local renditionCount = exportContext.exportSession:countRenditions()
    local galleryID, collectionExternalID, syncError = syncPublishedCollection(settings, exportContext)
    if not galleryID then
        for _, rendition in exportContext:renditions { stopIfCanceled = true } do
            rendition:uploadFailed(tostring(syncError))
        end
        return
    end
    local uploaded = 0
    for index, rendition in exportContext:renditions { stopIfCanceled = true } do
        local success, pathOrMessage = rendition:waitForRender()
        if success then
            progress:setCaption("Uploading " .. LrPathUtils.leafName(pathOrMessage))
            local caption = rendition.photo:getFormattedMetadata("caption") or ""
            local lens, lensError = Keywords.lensName(rendition.photo)
            if lensError then
                rendition:uploadFailed(lensError)
            else
                local photoExternalID = collectionExternalID .. ":" .. tostring(rendition.photo.localIdentifier)
                local result, err = API.upload(settings, galleryID, photoExternalID, pathOrMessage, caption, lens, function(portion)
                    if renditionCount > 0 then
                        progress:setPortionComplete(0.5 + (((index - 1) + portion) / renditionCount) * 0.5)
                    end
                end)
                if result then
                    rendition:recordPublishedPhotoId(tostring(result.id))
                    if result.url and result.url ~= "" then
                        rendition:recordPublishedPhotoUrl(result.url)
                    end
                    uploaded = uploaded + 1
                else
                    rendition:uploadFailed(tostring(err))
                end
            end
        else
            rendition:uploadFailed(pathOrMessage)
        end
    end
    if uploaded > 0 then
        local _, buildError = API.build(settings)
        if buildError then error(tostring(buildError)) end
    end
end

return provider
